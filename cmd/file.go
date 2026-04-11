package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/slack-go/slack"
	"github.com/tammersaleh/slack-cli/internal/output"
)

type FileCmd struct {
	List     FileListCmd     `cmd:"" help:"List files."`
	Info     FileInfoCmd     `cmd:"" help:"Show file details."`
	Download FileDownloadCmd `cmd:"" help:"Download a file."`
}

type FileListCmd struct {
	Limit   int    `help:"Page size." default:"20"`
	Cursor  string `help:"Continue from previous page."`
	All     bool   `help:"Fetch all pages."`
	Channel string `help:"Filter by channel ID or name."`
	User    string `help:"Filter by user ID."`
	Types   string `help:"Comma-separated file types (e.g. images,pdfs)."`
}

func (c *FileListCmd) Run(cli *CLI) error {
	if c.All && c.Cursor != "" {
		return &output.Error{Err: "invalid_input", Detail: "--all and --cursor are mutually exclusive", Code: output.ExitGeneral}
	}

	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()

	limit := c.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	channelID := c.Channel
	if channelID != "" {
		r := cli.NewResolver(client)
		resolved, err := r.ResolveChannel(ctx, channelID)
		if err == nil {
			channelID = resolved
		}
	}

	page := 1

	for {
		params := slack.GetFilesParameters{
			Channel: channelID,
			User:    c.User,
			Types:   c.Types,
			Count:   limit,
			Page:    page,
		}

		files, paging, err := client.Bot().GetFilesContext(ctx, params)
		if err != nil {
			return cli.ClassifyError(err)
		}

		for _, f := range files {
			if err := p.PrintItem(fileToMap(f)); err != nil {
				return err
			}
		}

		hasMore := paging != nil && paging.Page < paging.Pages
		if !c.All || !hasMore {
			nextCursor := ""
			if hasMore {
				nextCursor = base64.StdEncoding.EncodeToString(
					[]byte(fmt.Sprintf("page:%d", paging.Page+1)),
				)
			}
			return p.PrintMeta(output.Meta{
				HasMore:    hasMore,
				NextCursor: nextCursor,
			})
		}

		page++
	}
}

type FileInfoCmd struct {
	Files []string `arg:"" required:"" help:"File IDs."`
}

func (c *FileInfoCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()
	errorCount := 0

	for _, id := range c.Files {
		file, _, _, err := client.Bot().GetFileInfoContext(ctx, id, 0, 0)
		if err != nil {
			oErr := cli.ClassifyError(err)
			if oErr.Code != output.ExitGeneral {
				return oErr
			}
			errorCount++
			if err := p.PrintItem(map[string]any{
				"input":  id,
				"error":  oErr.Err,
				"detail": oErr.Detail,
			}); err != nil {
				return err
			}
			continue
		}

		m := fileToMap(*file)
		m["input"] = id
		if err := p.PrintItem(m); err != nil {
			return err
		}
	}

	meta := output.Meta{ErrorCount: errorCount}
	if err := p.PrintMeta(meta); err != nil {
		return err
	}
	if errorCount > 0 {
		return &output.ExitError{Code: output.ExitGeneral}
	}
	return nil
}

type FileDownloadCmd struct {
	File   string `arg:"" required:"" help:"File ID."`
	Output string `help:"Output path (default: original filename, '-' for stdout)." short:"o"`
}

func (c *FileDownloadCmd) Run(cli *CLI) error {
	client, err := cli.NewClient()
	if err != nil {
		return err
	}

	p := cli.NewPrinter()
	ctx := context.Background()

	file, _, _, err := client.Bot().GetFileInfoContext(ctx, c.File, 0, 0)
	if err != nil {
		return cli.ClassifyError(err)
	}

	if file.URLPrivateDownload == "" {
		return &output.Error{Err: "no_download_url", Detail: "File has no download URL", Code: output.ExitGeneral}
	}

	if c.Output == "-" {
		out := io.Writer(os.Stdout)
		if cli.out != nil {
			out = cli.out
		}
		if err := client.Bot().GetFileContext(ctx, file.URLPrivateDownload, out); err != nil {
			return cli.ClassifyError(err)
		}
		return nil
	}

	outPath := c.Output
	if outPath == "" {
		outPath = filepath.Base(file.Name)
	}
	if outPath == "" || outPath == "." {
		outPath = file.ID
	}

	f, err := os.Create(outPath)
	if err != nil {
		return &output.Error{Err: "file_error", Detail: err.Error(), Code: output.ExitGeneral}
	}

	if err := client.Bot().GetFileContext(ctx, file.URLPrivateDownload, f); err != nil {
		f.Close()
		_ = os.Remove(outPath)
		return cli.ClassifyError(err)
	}
	f.Close()

	if err := p.PrintItem(map[string]any{
		"input": c.File,
		"id":    file.ID,
		"name":  file.Name,
		"size":  file.Size,
		"path":  outPath,
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}
