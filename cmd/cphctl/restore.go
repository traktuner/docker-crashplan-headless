package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/restore"
)

var errRestoreCommand = errors.New("INVALID_RESTORE_COMMAND")

type restorePaths []string

func (p *restorePaths) String() string     { return "" }
func (p *restorePaths) Set(s string) error { *p = append(*p, s); return nil }
func restoreCommand(args []string, diagnostic io.Writer) (method, path string, body any, err error) {
	if len(args) == 0 {
		return "", "", nil, errRestoreCommand
	}
	switch args[0] {
	case "archive":
		if len(args) < 4 || args[1] != "--confirm" || args[2] != "ARCHIVE" {
			return "", "", nil, errRestoreCommand
		}
		for _, id := range args[3:] {
			if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
				return "", "", nil, errRestoreCommand
			}
		}
		return "POST", "restore/operations/archive", map[string]any{"requestIds": args[3:], "confirm": "ARCHIVE"}, nil
	case "roots", "jobs", "operations", "helper":
		if len(args) != 1 {
			return "", "", nil, errRestoreCommand
		}
		return "GET", "restore/" + args[0], nil, nil
	case "stop", "resume", "remove":
		if len(args) != 2 {
			return "", "", nil, errRestoreCommand
		}
		id, e := strconv.ParseInt(args[1], 10, 64)
		if e != nil || id <= 0 || strconv.FormatInt(id, 10) != args[1] {
			return "", "", nil, errRestoreCommand
		}
		if args[0] == "remove" {
			return "DELETE", "restore/jobs/" + args[1], nil, nil
		}
		action := args[0]
		if action == "resume" {
			action = "start"
		}
		return "POST", "restore/jobs/" + args[1] + "/" + action, struct{}{}, nil
	case "browse", "versions", "contents", "search", "start", "sets":
	default:
		return "", "", nil, errRestoreCommand
	}
	flags := flag.NewFlagSet("restore "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var q engine.ArchiveQuery
	flags.StringVar(&q.SourceID, "source", "", "source device GUID")
	flags.StringVar(&q.TargetID, "destination", "", "backup destination GUID")
	flags.StringVar(&q.BackupSetID, "set", "", "backup set ID")
	flags.StringVar(&q.At, "at", "0", "version cutoff in epoch milliseconds; 0 selects latest")
	flags.BoolVar(&q.IncludeDeleted, "include-deleted", false, "include deleted files")
	var req restore.StartRequest
	var paths restorePaths
	var files, folders, excludedFiles, excludedFolders restorePaths
	var folder bool
	var excluded restorePaths
	var pattern string
	var fullPath bool
	var original bool
	if args[0] == "start" {
		flags.StringVar(&req.Target.RootID, "root", "", "configured writable root ID")
		flags.StringVar(&req.Target.Relative, "relative", "", "existing relative destination directory")
		flags.StringVar(&req.Existing, "conflict", "RENAME_ORIGINAL", "existing file strategy")
		flags.StringVar(&req.Permissions, "permissions", "current", "current or original permissions")
		flags.StringVar(&req.RequestID, "request-id", "", "reuse the same request ID to retrieve an earlier submission")
		flags.Var(&paths, "path", "selected archive path; repeat for multiple paths")
		flags.Var(&files, "file", "selected archive file; repeat for multiple files")
		flags.Var(&folders, "directory", "selected archive directory; repeat for multiple directories")
		flags.Var(&excludedFiles, "exclude-file", "excluded descendant file")
		flags.Var(&excludedFolders, "exclude-directory", "excluded descendant directory")
		flags.BoolVar(&folder, "folder", false, "all selected paths are folders")
		flags.Var(&excluded, "exclude", "excluded descendant path; repeat for multiple exclusions")
		flags.BoolVar(&original, "original", false, "restore to explicit original parents inside configured writable roots")
	} else {
		flags.StringVar(&q.FileID, "file-id", "", "archive file ID; omit for the root")
		flags.StringVar(&q.Cursor, "cursor", "", "opaque page cursor")
		flags.IntVar(&q.Limit, "limit", 200, "page size, up to 1000")
	}
	if args[0] == "search" {
		flags.StringVar(&pattern, "pattern", "", "filename pattern with * and ?")
		flags.BoolVar(&fullPath, "full-path", false, "match the complete archive path")
	}
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return "", "", nil, errRestoreCommand
	}
	if args[0] == "sets" {
		if !nativeID(q.SourceID) || !nativeID(q.TargetID) {
			return "", "", nil, errRestoreCommand
		}
		return "POST", "restore/sets", map[string]string{"sourceId": q.SourceID, "targetId": q.TargetID}, nil
	}
	if q.Validate() != nil {
		return "", "", nil, errRestoreCommand
	}
	if args[0] == "search" {
		if pattern == "" {
			return "", "", nil, errRestoreCommand
		}
		return "POST", "restore/search", engine.ArchiveSearch{ArchiveQuery: q, Pattern: pattern, FullPath: fullPath}, nil
	}
	if args[0] != "start" {
		return "POST", "restore/" + args[0], q, nil
	}
	req.SourceID = q.SourceID
	req.TargetID = q.TargetID
	req.IncludeDeleted = q.IncludeDeleted
	for _, path := range paths {
		req.Items = append(req.Items, engine.RestoreSelection{Path: path, BackupSetID: q.BackupSetID, Timestamp: q.At, IsFolder: folder})
	}
	for _, path := range files {
		req.Items = append(req.Items, engine.RestoreSelection{Path: path, BackupSetID: q.BackupSetID, Timestamp: q.At})
	}
	for _, path := range folders {
		req.Items = append(req.Items, engine.RestoreSelection{Path: path, BackupSetID: q.BackupSetID, Timestamp: q.At, IsFolder: true})
	}
	for _, path := range excluded {
		req.Excluded = append(req.Excluded, engine.RestoreSelection{Path: path, BackupSetID: q.BackupSetID, Timestamp: q.At, IsFolder: folder})
	}
	for _, path := range excludedFiles {
		req.Excluded = append(req.Excluded, engine.RestoreSelection{Path: path, BackupSetID: q.BackupSetID, Timestamp: q.At})
	}
	for _, path := range excludedFolders {
		req.Excluded = append(req.Excluded, engine.RestoreSelection{Path: path, BackupSetID: q.BackupSetID, Timestamp: q.At, IsFolder: true})
	}
	if !original && (engine.RestoreSubmission{SourceID: req.SourceID, TargetID: req.TargetID, Destination: "/restore", Existing: req.Existing, Permissions: req.Permissions, Items: req.Items, Excluded: req.Excluded}).Validate() != nil {
		return "", "", nil, errRestoreCommand
	}
	root, e := strconv.Atoi(req.Target.RootID)
	if !original && (e != nil || root < 0 || strconv.Itoa(root) != req.Target.RootID) {
		return "", "", nil, errRestoreCommand
	}
	req.RequestID, err = newRequestID(req.RequestID)
	if err != nil {
		return "", "", nil, errRestoreCommand
	}
	fmt.Fprintln(diagnostic, "RESTORE_REQUEST_ID", req.RequestID)
	if original {
		return "POST", "restore/original", restore.OriginalRequest{RequestID: req.RequestID, SourceID: req.SourceID, TargetID: req.TargetID, Existing: req.Existing, Permissions: req.Permissions, IncludeDeleted: req.IncludeDeleted, Items: req.Items, Excluded: req.Excluded}, nil
	}
	return "POST", "restore/jobs", req, nil
}
