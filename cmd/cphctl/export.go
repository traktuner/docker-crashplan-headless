package main

import (
	"context"
	"fmt"
	"github.com/traktuner/crashplan-headless/internal/ctl"
	"io"
	"os"
)

func exportArchiveKey(client *ctl.Client, path string, out, diagnostic io.Writer) int {
	return saveKeyDownload(path, func() ([]byte, error) { return client.ExportArchiveKey(context.Background()) }, out, diagnostic)
}
func exportDerivedKey(client *ctl.Client, path string, input io.Reader, out, diagnostic io.Writer) int {
	raw, ok := readPrivateJSON(input)
	if !ok {
		return 2
	}
	return saveKeyDownload(path, func() ([]byte, error) { return client.ExportDerivedKey(context.Background(), raw) }, out, diagnostic)
}
func saveKeyDownload(path string, download func() ([]byte, error), out, diagnostic io.Writer) int {
	if _, err := os.Lstat(path); err == nil {
		fmt.Fprintln(diagnostic, "KEY_EXPORT_DESTINATION_EXISTS")
		return 2
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(diagnostic, "KEY_EXPORT_DESTINATION_UNAVAILABLE")
		return 2
	}
	raw, err := download()
	if err != nil {
		fmt.Fprintln(diagnostic, err.Error())
		return 3
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fmt.Fprintln(diagnostic, "KEY_EXPORT_DESTINATION_UNAVAILABLE")
		return 2
	}
	n, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if n != len(raw) || writeErr != nil || syncErr != nil || closeErr != nil {
		fmt.Fprintln(diagnostic, "KEY_EXPORT_INCOMPLETE_PRIVATE_FILE")
		return 3
	}
	fmt.Fprintln(out, "Custom archive key saved to the private file.")
	return 0
}
