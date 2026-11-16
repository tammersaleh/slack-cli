package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/api"
	"github.com/tammersaleh/slack-cli/internal/output"
)

// draftFile is the part of a Slack file object a draft row shows. Slack sends
// the full files.info shape in the top-level `files` array of drafts.list,
// drafts.create, and drafts.update; the rest is dropped.
type draftFile struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Title    string `json:"title,omitempty"`
	Size     int64  `json:"size"`
	Filetype string `json:"filetype,omitempty"`
	Mimetype string `json:"mimetype,omitempty"`
}

// joinDraftFiles attaches to each draft the file objects one response carried
// for its file_ids, in file_ids order. Slack's `files` array is deduplicated
// across the drafts of the response and omits a deleted file, so an id with no
// object stays in file_ids only.
func joinDraftFiles(drafts []draftData, files []draftFile) {
	if len(files) == 0 {
		return
	}
	byID := make(map[string]draftFile, len(files))
	for _, f := range files {
		byID[f.ID] = f
	}
	for i := range drafts {
		for _, id := range drafts[i].FileIDs {
			if f, ok := byID[id]; ok {
				drafts[i].files = append(drafts[i].files, f)
			}
		}
	}
}

// parseDraftWrite decodes a drafts.create or drafts.update answer,
// {ok, draft, files}, into the draft with its file objects joined.
func parseDraftWrite(data []byte, method string) (draftData, error) {
	var resp struct {
		Draft draftData   `json:"draft"`
		Files []draftFile `json:"files"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return draftData{}, &output.Error{Err: "parse_error", Detail: "Failed to parse " + method + " response", Code: output.ExitGeneral}
	}
	drafts := []draftData{resp.Draft}
	joinDraftFiles(drafts, resp.Files)
	return drafts[0], nil
}

// validateDraftFiles checks every --file path before any request: each must
// be a readable regular file with content. Size and type limits are Slack's to
// enforce.
func validateDraftFiles(paths []string) error {
	reject := func(path, why string) error {
		return &output.Error{Err: "invalid_input", Detail: fmt.Sprintf("--file %s: %s", path, why), Code: output.ExitGeneral}
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		switch {
		case err != nil:
			return &output.Error{Err: "invalid_input", Detail: "--file: " + err.Error(), Code: output.ExitGeneral}
		case !info.Mode().IsRegular():
			return reject(path, "not a regular file")
		case info.Size() == 0:
			return reject(path, "the file is empty; Slack rejects a zero-length upload")
		}
		// An unreadable file would otherwise fail after Slack had already
		// handed out a file id for it.
		f, err := os.Open(path)
		if err != nil {
			return &output.Error{Err: "invalid_input", Detail: "--file: " + err.Error(), Code: output.ExitGeneral}
		}
		_ = f.Close()
	}
	return nil
}

// fileIDsJSON encodes ids as the file_ids form value. It is "[]" for none:
// drafts.create answers invalid_arguments when the param is omitted.
func fileIDsJSON(ids []string) (string, error) {
	if len(ids) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "", &output.Error{Err: "encode_error", Detail: err.Error(), Code: output.ExitGeneral}
	}
	return string(b), nil
}

// uploadFiles uploads each path through Slack's three-step external upload
// (files.getUploadURLExternal, a multipart POST to the returned URL,
// files.completeUploadExternal with no channel) and returns the file ids in
// path order. The result is a file with no shares, visible to the uploader.
//
// allocated holds every id Slack handed out, including one whose upload did
// not complete, so the caller can delete them all when err is non-nil. Each
// step runs exactly once: an upload is not safe to repeat blind.
func uploadFiles(ctx context.Context, client *api.Client, paths []string) (allocated []string, err error) {
	bot := client.Bot()
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return allocated, err
		}
		name := filepath.Base(path)

		var target *slack.GetUploadURLExternalResponse
		err = api.CallOnce(ctx, "files.getUploadURLExternal", func() error {
			var err error
			target, err = bot.GetUploadURLExternalContext(ctx, slack.GetUploadURLExternalParameters{
				FileName: name,
				FileSize: int(info.Size()),
			})
			return err
		})
		if err != nil {
			return allocated, err
		}
		allocated = append(allocated, target.FileID)

		err = api.CallOnce(ctx, "files.upload", func() error {
			return bot.UploadToURL(ctx, slack.UploadToURLParameters{UploadURL: target.UploadURL, File: path})
		})
		if err != nil {
			return allocated, err
		}

		err = api.CallOnce(ctx, "files.completeUploadExternal", func() error {
			_, err := bot.CompleteUploadExternalContext(ctx, slack.CompleteUploadExternalParameters{
				Files: []slack.FileSummary{{ID: target.FileID, Title: name}},
			})
			return err
		})
		if err != nil {
			return allocated, err
		}
	}
	return allocated, nil
}

// uploadCleanupTimeout bounds the deletes that follow a failure. They run on
// their own deadline because the command's may be what expired.
const uploadCleanupTimeout = 10 * time.Second

// deleteUploadedFiles deletes files this invocation uploaded and returns the
// ids it could not delete. file_not_found counts as deleted: it is what an id
// whose upload never completed has to answer, and there is nothing left.
func deleteUploadedFiles(ctx context.Context, client *api.Client, ids []string) (left []string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), uploadCleanupTimeout)
	defer cancel()
	for _, id := range ids {
		err := api.CallOnce(ctx, "files.delete", func() error {
			return client.Bot().DeleteFileContext(ctx, id)
		})
		if err == nil {
			continue
		}
		if code, _ := api.SlackErrorCode(err); code == "file_not_found" {
			continue
		}
		left = append(left, id)
	}
	return left
}

// slackRefused reports whether Slack answered a draft write with an
// application-level refusal, which means the write did not take effect. A
// rate limit, a timeout, a network failure, and a 5xx all leave that unknown.
// A rate limit never qualifies: the internal-API transport reports both of its
// shapes (a 429, and an ok:false body naming the limit) as
// *slack.RateLimitedError, which carries no Slack error code.
func slackRefused(err error) bool {
	_, refused := api.SlackErrorCode(err)
	return refused
}

// withDetail returns a copy of e with note appended to its detail.
func withDetail(e *output.Error, note string) *output.Error {
	out := *e
	if out.Detail != "" {
		out.Detail += " "
	}
	out.Detail += note
	return &out
}

// uploadDraftFiles uploads the --file paths for a draft write. On failure no
// draft write has happened, so nothing can reference the files: every id
// handed out is deleted, and the classified error names any that could not be.
func (c *CLI) uploadDraftFiles(ctx context.Context, client *api.Client, paths []string) ([]string, error) {
	ids, err := uploadFiles(ctx, client, paths)
	if err == nil {
		return ids, nil
	}
	oErr := c.ClassifyError(err)
	if left := deleteUploadedFiles(ctx, client, ids); len(left) > 0 {
		oErr = withDetail(oErr, "Uploaded files that could not be deleted: "+strings.Join(left, ", ")+".")
	}
	return nil, oErr
}

// settleUploads decides what happens to the files this invocation uploaded
// once the draft write has failed, and returns report with the outcome in its
// detail. writeErr is the raw error of the draft write; report is the error
// the command is about to return, which on update can be a verdict from the
// re-fetch instead of the write's own error.
//
// A refusal from Slack means no draft references the files, so they are
// deleted. Anything else leaves the write's outcome unknown: the draft may
// exist, and deleting would leave it a dangling file id, so the files stay
// and the detail names them.
func settleUploads(ctx context.Context, client *api.Client, writeErr error, report *output.Error, uploaded []string) *output.Error {
	if len(uploaded) == 0 {
		return report
	}
	if !slackRefused(writeErr) {
		return withDetail(report, "The draft write may or may not have taken effect, so the uploaded files were left in place: "+
			strings.Join(uploaded, ", ")+". Check 'slack draft list' before retrying.")
	}
	if left := deleteUploadedFiles(ctx, client, uploaded); len(left) > 0 {
		return withDetail(report, "Uploaded files that could not be deleted: "+strings.Join(left, ", ")+".")
	}
	return report
}
