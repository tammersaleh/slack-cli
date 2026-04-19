package cmd

import (
	"os"
	"path/filepath"
	"time"

	"github.com/tammersaleh/slack-cli/internal/output"
)

type CacheCmd struct {
	Info  CacheInfoCmd  `cmd:"" help:"Show cache file info."`
	Clear CacheClearCmd `cmd:"" help:"Delete all cache files."`
}

type CacheInfoCmd struct{}

func (c *CacheInfoCmd) Run(cli *CLI) error {
	p := cli.NewPrinter()
	dir := cacheDir()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return p.PrintMeta(output.Meta{})
		}
		return &output.Error{Err: "cache_error", Detail: err.Error(), Code: output.ExitGeneral}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		age := time.Since(info.ModTime())
		if err := p.PrintItem(map[string]any{
			"file":     entry.Name(),
			"path":     filepath.Join(dir, entry.Name()),
			"size":     info.Size(),
			"modified": info.ModTime().UTC().Format(time.RFC3339),
			"age":      age.Round(time.Second).String(),
		}); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}

type CacheClearCmd struct{}

func (c *CacheClearCmd) Run(cli *CLI) error {
	p := cli.NewPrinter()
	dir := cacheDir()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return p.PrintMeta(output.Meta{})
		}
		return &output.Error{Err: "cache_error", Detail: err.Error(), Code: output.ExitGeneral}
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err == nil {
			removed++
		}
	}

	if err := p.PrintItem(map[string]any{
		"removed": removed,
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

func cacheDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "slack-cli", "cache")
	}
	return ""
}
