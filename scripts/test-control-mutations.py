#!/usr/bin/env python3
"""Check selected control and storage guards in isolated source copies.

Requires Python3 and the repository's Go compiler. Run from any directory.
Build web/ first when the UI embed is part of a selected package's dependency.
The checkout is never mutated. --output stores safe test logs and a JSON report.
Failures, surviving mutations and compilation failures return a nonzero exit.
This command covers named guards; it is not exhaustive syscall fault injection.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import tempfile


# name, relative source file, package, original text, weakened text
MUTATIONS = [
    ("scan-stalled-barrier", "internal/engine/scan.go", "./internal/engine", ' || state == "stalled"', ''),
    ("scan-session-revision", "internal/engine/scan.go", "./internal/engine", 'c.BindSession(ctx, saved.Revision)', 'c.BindSession(ctx, c.SessionRevision())'),
    ("scan-native-identity", "internal/engine/scan.go", "./internal/engine", 'identity = &accountIdentity{user: saved.User, self: saved.Self}', 'identity = nil'),
    ("scan-journal-attribution", "internal/engine/scan.go", "./internal/engine", 'Binding: op.binding', 'Binding: nil'),
    ("scan-legacy-context", "internal/engine/scan.go", "./internal/engine", 'if saved == nil {\n\t\treturn nil, ErrSessionChanged\n\t}', 'if saved == nil {\n\t\treturn c.operationContext(ctx), nil\n\t}'),
    ("scan-invalid-context", "internal/engine/scan.go", "./internal/engine", 'op.binding != nil && (op.binding.Revision == 0 || !validID(op.binding.User) || !validID(op.binding.Self))', 'false'),
    ("config-image-layout", "internal/config/config.go", "./internal/config", 'dir != "" && dir != "/config"', '(dir != "" && dir != "/config") && false'),
    ("state-parent-symlink", "internal/statefile/store.go", "./internal/statefile", "syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC", "syscall.O_DIRECTORY|syscall.O_CLOEXEC"),
    ("state-record-symlink", "internal/statefile/store.go", "./internal/statefile", "syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK", "syscall.O_RDONLY|syscall.O_NONBLOCK"),
    ("state-parent-permissions", "internal/statefile/store.go", "./internal/statefile", "info.Mode().Perm() == 0700", "true"),
    ("state-record-permissions", "internal/statefile/store.go", "./internal/statefile", "info.Mode().Perm() == 0600", "true"),
    ("state-record-links", "internal/statefile/store.go", "./internal/statefile", "stat.Nlink == 1", "true"),
    ("state-record-type", "internal/statefile/store.go", "./internal/statefile", "info.Mode().IsRegular()", "true"),
    ("state-parent-owner", "internal/statefile/store.go", "./internal/statefile", "info.IsDir() && info.Mode().Perm() == 0700 && stat.Uid == uint32(os.Geteuid())", "info.IsDir() && info.Mode().Perm() == 0700 && stat.Uid >= 0"),
    ("state-record-owner", "internal/statefile/store.go", "./internal/statefile", "stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1", "stat.Uid >= 0 && stat.Nlink == 1"),
    ("state-read-limit", "internal/statefile/store.go", "./internal/statefile", "if err != nil || len(raw) > maxRecord {", "if err != nil {"),
    ("state-write-limit", "internal/statefile/store.go", "./internal/statefile", "s.dir == nil || len(raw) > maxRecord", "s.dir == nil"),
    ("lock-symlink", "internal/statefile/store.go", "./internal/statefile", "syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK", "syscall.O_RDWR|syscall.O_CREAT|syscall.O_NONBLOCK"),
    ("socket-exclusive-lock", "internal/statefile/store.go", "./internal/controlsocket", "syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil", "false"),
    ("socket-file-type", "internal/controlsocket/listener.go", "./internal/controlsocket", "info.Mode()&os.ModeSocket == 0", "false"),
    ("socket-links", "internal/controlsocket/listener.go", "./internal/controlsocket", "stat.Nlink != 1", "false"),
    ("socket-close-identity", "internal/controlsocket/listener.go", "./internal/controlsocket", "os.SameFile(current, l.identity)", "os.SameFile(current, l.identity) || current != nil"),
    ("config-mounted", "internal/initcfg/prepare.go", "./internal/initcfg", "if err := sources.LocalMountIn(raw, options.Root); err != nil {", "if err := sources.LocalMountIn(raw, options.Root); false && err != nil {"),
    ("config-preserve-heap", "internal/initcfg/prepare.go", "./internal/initcfg", 'if options.Heap != "" {', "if true {"),
    ("config-cache-delete-regression", "internal/initcfg/prepare.go", "./internal/initcfg", 'marker := filepath.Join(options.Root, "cp_version")', 'os.RemoveAll(filepath.Join(options.Root, "cache"))\n marker := filepath.Join(options.Root, "cp_version")'),
    ("source-dataset-root", "internal/sources/gate.go", "./internal/sources", 'if mount.Root != "/" && sentinel == "" {', "if false {"),
    ("source-ascii-fields-regression", "internal/sources/gate.go", "./internal/sources", 'strings.Split(scanner.Text(), " ")', "strings.Fields(scanner.Text())"),
    ("scan-durable-before-dispatch", "internal/engine/scan.go", "./internal/engine", "s.operations = append(s.operations, op)\n\tif err := s.save(); err != nil {", "s.operations = append(s.operations, op)\n\tif err := error(nil); err != nil {"),
    ("scan-one-pending", "internal/engine/scan.go", "./internal/engine", "if op.BackupSetID == setID && pending(op.State) {", 'if op.BackupSetID == "" && false {'),
    ("scan-later-completion", "internal/engine/scan.go", "./internal/engine", "!stats.Scanning && stats.LastCompleted > op.Baseline &&", "!stats.Scanning && stats.LastCompleted >= op.Baseline &&"),
    ("scan-generation", "internal/engine/scan.go", "./internal/engine", "if op.Generation != generation || op.EngineVariant != stats.EngineVariant {", "if op.Generation == generation && false {"),
    ("scan-native-duration", "internal/engine/scan.go", "./internal/engine", 'stats.Duration >= 0 && (stats.EngineVariant != "v3" || stats.Duration > 0) && ', ""),
    ("scan-request-time", "internal/engine/scan.go", "./internal/engine", ' && stats.LastCompleted-stats.Duration >= op.Created.UnixMilli()-2000', ""),
    ("native-token-generation", "internal/engine/client.go", "./internal/engine", "marker != nil && (ctx.Err() != nil", "false && (ctx.Err() != nil"),
    ("restore-session-once", "internal/lifecycle/owner.go", "./internal/lifecycle", "if ready && !attemptedRestore {", "if ready && (!attemptedRestore || time.Since(lastScan) >= 0) {"),
    ("mutation-client-context", "internal/lifecycle/owner.go", "./internal/lifecycle", "revision: o.options.Native.SessionRevision(), fn: fn,", "revision: o.options.Native.SessionRevision(), fn: func(context.Context)error{return fn(wait)},"),
    ("console-symlink", "internal/logx/console.go", "./internal/logx", "os.O_APPEND|syscall.O_NOFOLLOW", "os.O_APPEND|(syscall.O_NOFOLLOW & 0)"),
    ('restore-marker-exclusive', 'internal/restore/files.go', './internal/restore', 'syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW', 'syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NOFOLLOW'),
    ('restore-marker-cleanup-identity', 'internal/restore/files.go', './internal/restore', '!os.SameFile(info, m.identity)', 'false'),
    ('restore-directory-root-identity', 'internal/restore/files.go', './internal/restore', '!os.SameFile(info, r.identity)', 'false'),
    ('restore-directory-destination-identity', 'internal/restore/files.go', './internal/restore', '!os.SameFile(info, d.identity)', 'false'),
    ('restore-directory-symlink', 'internal/restore/files.go', './internal/restore', 'for _, component := range strings.Split(relative, "/") {\n\t\tnext, e := syscall.Openat(int(current.Fd()), component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)', 'for _, component := range strings.Split(relative, "/") {\n\t\tnext, e := syscall.Openat(int(current.Fd()), component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)'),
    ('restore-helper-user', 'internal/restore/helper.go', './internal/restore', 'if m.User != h.options.UserName {', 'if m.User=="" && false {'),
    ('restore-helper-parent', 'internal/restore/helper.go', './internal/restore', '!os.SameFile(info, g.dataIdentity)', '!os.SameFile(info, g.dataIdentity) && info==nil'),
    ('restore-helper-fifo-type', 'internal/restore/helper.go', './internal/restore', 'info.Mode()&os.ModeNamedPipe == 0', 'false'),
    ('restore-helper-binary-hash', 'internal/restore/helper.go', './internal/restore', 'hex.EncodeToString(sum.Sum(nil)) != h.options.ExpectedSHA', 'false'),
    ('restore-helper-imported-job', 'internal/restore/helper.go', './internal/restore', 'err = h.admit(ctx)', 'err = error(nil); _ = ctx'),

    ("restore-helper-null-user", "internal/engine/restore.go", "./internal/engine", 'j.kind == "CLIENT" && j.hasUser', 'j.kind == "CLIENT" && j.userName != ""'),

    ('account-request-timeout', 'internal/engine/client.go', './internal/engine', 'copy.Timeout = deadline', 'copy.Timeout = c.http.Timeout'),
    ('login-retry-only-native', 'internal/engine/login.go', './internal/engine', 'ids[0] == "REATTEMPT_LOGIN"', 'ids[0] != "NEVER_RETRY"'),
    ('security-confirmation', 'internal/engine/security.go', './internal/engine', 'in.Confirm != "UPGRADE TO "+in.Type', 'false'),
    ('security-no-repeat-upgrade', 'internal/engine/security.go', './internal/engine', 'securityRank(in.Type) <= securityRank(before.Type)', 'securityRank(in.Type) <= securityRank(before.Type) && false'),
    ('security-key-length', 'internal/engine/security.go', './internal/engine', 'len(b) == 56', 'len(b) > 0'),
    ('security-session-key-reset', 'internal/engine/client.go', './internal/engine', 'c.token = token\n\tc.migration = nil\n\tc.archiveKeys = nil', 'c.token = token\n\tc.migration = nil'),
    ('security-generation-key-reset', 'internal/engine/client.go', './internal/engine', 'c.lifetime = ctx\n\tc.migration = nil\n\tc.archiveKeys = nil', 'c.lifetime = ctx\n\tc.migration = nil'),
    ('native-error-message-field', 'internal/engine/response.go', './internal/engine', 'key != "errorMessage" && strings.HasSuffix', 'strings.HasSuffix'),
    ('restore-excluded-not-selected', 'internal/engine/restore.go', './internal/engine', 'selection{item.BackupSetID, item.Timestamp, item.IsFolder, false}', 'selection{item.BackupSetID, item.Timestamp, item.IsFolder, true}'),
    ('restore-duplicate-names', 'internal/engine/restore.go', './internal/engine', 'if names[name] {', 'if false {'),
    ('original-request-digest', 'internal/restore/original.go', './internal/restore', 'if r.Digest != digest {', 'if false {'),
    ('original-explicit-parent', 'internal/restore/original.go', './internal/restore', 'filepath.Rel(root, parent)', 'filepath.Rel(root, root)'),
    ('original-terminal-child', 'internal/restore/original.go', './internal/restore', 'if op.State != "accepted" {', 'if op.State != "accepted" && false {'),
    ('archive-replay-barrier', 'internal/restore/service.go', './internal/restore', '} else if found {', '} else if false && found {'),
    ('archive-digest-binding', 'internal/restore/retention.go', './internal/restore', 'if r.Digest != digest {', 'if false {'),
    ('archive-active-job', 'internal/restore/retention.go', './internal/restore', 'present && !done', 'present && !done && false'),
    ('archive-batch-children', 'internal/restore/retention.go', './internal/restore', 'if !selected[child] {', 'if !selected[child] && false {'),
    ('archive-durable-barrier', 'internal/restore/retention.go', './internal/restore', 'if s.store.Write("restore-saved-"+r.RequestID+".json", raw) != nil {', 'if false && s.store.Write("restore-saved-"+r.RequestID+".json", raw) != nil {'),
    ('archive-index-save', 'internal/restore/retention.go', './internal/restore', 'if err := s.save(); err != nil {', 'if err := error(nil); err != nil {'),

    ('key-export-file-exclusive', 'cmd/cphctl/export.go', './cmd/cphctl', 'os.O_WRONLY|os.O_CREATE|os.O_EXCL', 'os.O_WRONLY|os.O_CREATE'),

    ('local-policy', 'internal/destinations/service.go', './internal/destinations', 'if (sourceDevice.Self && !policy.Allowed) || (!sourceDevice.Self && (!sourceDevice.Own || !ownedAllowed)) {', 'if false {'),
    ('local-replay', 'internal/destinations/service.go', './internal/destinations', '\t\t\treturn saved, nil', '\t\t\tsaved.State = "ignored"'),
    ('local-digest', 'internal/destinations/service.go', './internal/destinations', 'saved.Digest != digest', 'false'),
    ('local-journal-before', 'internal/destinations/service.go', './internal/destinations', 'if err := save(); err != nil {', 'if err := error(nil); err != nil {'),
    ('local-confirmation', 'internal/destinations/service.go', './internal/destinations', 'in.Confirm != confirm', 'in.Confirm != confirm && false'),
    ('local-move-source', 'internal/destinations/service.go', './internal/destinations', '} else if validate(filepath.Join(path, source)) != nil {', '} else if validate(filepath.Join(path, source)) != nil && false {'),
    ('local-final-admission', 'internal/engine/localarchive.go', './internal/engine', 'if validate(path) != nil {', 'if false {'),
    ('archive-child-symlink', 'internal/restore/files.go', './internal/destinations', 'c.name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC', 'c.name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC'),
    ('archive-child-identity', 'internal/restore/files.go', './internal/restore', '(c.identity != nil && !os.SameFile(info, c.identity))', 'false'),
    ('archive-require-absence', 'internal/restore/files.go', './internal/restore', 'err != nil || missingOnly || !info.IsDir()', 'err != nil || !info.IsDir()'),
    ('device-remove-self', 'internal/destinations/devices.go', './internal/destinations', '(device.Self || device.ParentID != policy.SelfID)', 'false'),
    ('device-remove-locked', 'internal/destinations/devices.go', './internal/destinations', '(set.Locked || set.DestinationsLocked || destination.Locked)', 'false'),
    ('device-remove-parent', 'internal/destinations/devices.go', './internal/destinations', 'err == nil && parent != nil && (parent.Validate() != nil || !parent.Writable())', 'false'),
    ('registration-no-key', 'internal/engine/setup.go', './internal/engine', 'strings.TrimSpace(configuration.RegistrationKey) == ""', 'strings.TrimSpace(configuration.RegistrationKey) == "" && false'),
    ('helper-ipc-outside', 'agent/src/cph/RestoreBoundary.java', 'java', 'if (selected == null) deny();', 'if (selected == null) return;'),
    ('helper-ipc-canonical', 'agent/src/cph/RestoreBoundary.java', 'java', '!path.normalize().equals(path)', 'false'),
    ('helper-ipc-symlink', 'agent/src/cph/RestoreBoundary.java', 'java', 'if (attrs.isSymbolicLink() && !(linkObject && current.equals(path))) deny();', 'if (false) deny();'),
    ('helper-ipc-root-identity', 'agent/src/cph/RestoreBoundary.java', 'java', '!pinned.equals(attrs.fileKey())', 'false'),
    ('restore-link-parent', 'agent/src/cph/RestoreBoundary.java', 'java', '!(linkObject && current.equals(path))', '!linkObject'),
    ('restore-compressed-output', 'agent/src/cph/Patches.java', 'java', 'cb -> cb.aload(1).aload(2).invokestatic(ClassDesc.of("cph.RestoreBoundary"), "files",\n                MethodTypeDesc.ofDescriptor("(Ljava/io/File;Ljava/io/File;)V")), true)', 'cb -> cb.nop(), true)'),
    ('sso-org-context', 'internal/engine/setup.go', './internal/engine', 'in.OrganizationKey = org', 'in.OrganizationKey = ""'),
    ('maintenance-connected', 'internal/engine/maintenance.go', './internal/engine', 'if !connected {', 'if !connected && false {'),
    ('maintenance-existing-client', 'internal/engine/maintenance.go', './internal/engine', 'if wire.Stats == nil {', 'if wire.Stats == nil { return nil }; if wire.Stats == nil {'),
    ('maintenance-nonempty', 'internal/engine/maintenance.go', './internal/engine', 'if size <= 0 {', 'if size <= 0 && false {'),
    ('custom-key-confirmation', 'internal/engine/custom_keys.go', './internal/engine', 'if in.Confirm != "REPLACE CUSTOM KEY" {', 'if false {'),
    ('custom-key-readback', 'internal/engine/custom_keys.go', './internal/engine', 'err != nil || subtle.ConstantTimeCompare([]byte(key), []byte(after.key)) != 1', 'err != nil || (after.key == "" && false)'),
    ('custom-key-cache-clear', 'internal/engine/custom_keys.go', './internal/engine', 'c.clearArchiveKeys()', '_ = c'),
    ('derived-key-ownership', 'internal/engine/custom_keys.go', './internal/engine', '!allowed || !validID(policy.UserID)', '(!allowed && false) || !validID(policy.UserID)'),
    ('derived-key-session', 'internal/engine/custom_keys.go', './internal/engine', '!c.sessionCurrent(ctx) || ctx.Err() != nil', '(!c.sessionCurrent(ctx) && false) || ctx.Err() != nil'),
    ('derived-key-blank', 'internal/engine/custom_keys.go', './internal/engine', 'nativeBlankPassphrase(in.Passphrase)', 'in.Passphrase == ""'),
    ('maintenance-target-field', 'internal/engine/maintenance.go', './internal/engine', 'map[string]string{"targetId": target}', 'map[string]string{"value": target}'),
    ('maintenance-native-connection', 'internal/engine/localarchive.go', './internal/engine', 'd.Properties["DCODE"] == "CONNECTED"', 'd.Properties["DCODE"] == "DISCONNECTED"'),
    ('restore-relative-link-text', 'agent/src/cph/RestoreBoundary.java', 'java', 'file.getPath() : file.getAbsolutePath()', 'file.getAbsolutePath() : file.getAbsolutePath()'),
    ('legacy-uncertainty-barrier', 'internal/engine/restore.go', './internal/engine', 'if uncertain {', 'if uncertain && false {'),
    ('legacy-generation-reset', 'internal/engine/client.go', './internal/engine', 'c.legacy = nil', '_ = c.legacy'),
    ('adoption-native-state', 'internal/engine/localarchive.go', './internal/destinations', 'out.CanAttachOwn = state.InProgress', 'out.CanAttachOwn = state.InProgress || true'),
    ('adoption-final-admission', 'internal/destinations/service.go', './internal/destinations', 'if !sourceDevice.Self && !policy.OwnDevices {', 'if false {'),
    ('replacement-confirmation', 'internal/engine/replacement.go', './internal/engine', 'confirm != strings.ToUpper(action)+" REPLACEMENT"', '(confirm != strings.ToUpper(action)+" REPLACEMENT" && false)'),
    ('replacement-readback', 'internal/engine/replacement.go', './internal/engine', 'err != nil || after.InProgress || after.Available || after.Selected != "" || after.Screen != "" || token != c.nativeToken() || !c.currentGeneration(ctx)', 'err != nil || (after.InProgress && false)'),
    ('session-before-dispatch', 'internal/engine/client.go', './internal/engine', 'if !c.sessionCurrentLocked(ctx) {\n\t\tc.mu.RUnlock()', 'if false {\n\t\tc.mu.RUnlock()'),
    ('session-after-dispatch', 'internal/engine/operation_session.go', './internal/engine', 'return c.sessionCurrentLocked(ctx)', 'return true'),
    ('session-revision-aba', 'internal/engine/client.go', './internal/engine', 'c.revision++\n\tc.token = token\n\tc.migration = nil', 'c.token = token\n\tc.migration = nil'),
    ('owner-queued-session', 'internal/lifecycle/owner.go', './internal/lifecycle', 'BindSession(ctx, op.revision)', 'BindSession(ctx, o.options.Native.SessionRevision())'),
    ('sso-identity-confirmation', 'internal/engine/sso.go', './internal/engine', 'id != a.state.AssertionID || username != a.state.Username', 'false'),
    ('native-account-identity', 'internal/engine/identity.go', './internal/engine', 'current.user != pinned.user || current.self != allowed', 'current.user != pinned.user && current.self != allowed && false'),
    ('archive-session-credentials', 'internal/engine/archive.go', './internal/engine', 'credentials.revision != c.SessionRevision()', 'false'),
    ('contents-terminal-event', 'internal/engine/contents_stream.go', './internal/engine', 'terminal && snapshot.Done', 'snapshot.Done'),
    ('contents-retain-stream', 'internal/engine/contents_stream.go', './internal/engine', 'replace := active.closed && active.delivered', 'replace := true'),
    ('archive-query-echo', 'agent/src/cph/Patches.java', 'java', 'cb.ldc("cphNoExcludedQueryField")', 'cb.ldc("backupQueryData")'),
    ('discovery-login-lock', 'internal/engine/setup.go', './internal/engine', 'return LoginConfiguration{}, ErrLoginInProgress', 'return c.loginConfiguration(ctx, in)'),
    ('discovery-owner-queue', 'internal/app/server.go', './internal/app', 'var result engine.LoginConfiguration\n\t\tif err := s.accountMutation', 'var result engine.LoginConfiguration\n\t\tif err := func(_ context.Context, fn func(context.Context)error)error{return fn(r.Context())}'),
    ('helper-ipc-required', 'agent/src/cph/Agent.java', 'java', 'patch.name().startsWith("restore-")', 'false'),
    ('v3-request-id', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'context.requestId != wrapper.getRequestId()', 'false'),
    ('v3-request-session', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'context.session != wrapper.getSession()', 'false'),
    ('v3-request-kind', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'context.responseType != responseType', 'false'),
    ('v3-decoded-kind', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'wrapper.getMessage().getClass() != responseType', 'false'),
    ('v3-response-claim', 'agent/src/cph/archive/ArchiveRequests.java', 'java', '!context.claimed.compareAndSet(false, true)', 'false'),
    ('v3-response-expiry', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'context.expired() || context.failed() || !context.claimed', 'context.failed() || !context.claimed'),
    ('v3-response-send-failure', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'context.expired() || context.failed() || !context.claimed', 'context.expired() || !context.claimed'),
    ('v3-send-future', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'context.sendResult = session.sendMessageWithFuture(message);', 'session.sendMessage(message);'),
    ('v3-registry-cap', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'pending >= MAX_PENDING', 'false'),
    ('v3-registry-expiry', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'typed.expired() || typed.failed()', 'typed.failed()'),
    ('v3-registry-close', 'agent/src/cph/archive/ArchiveRequests.java', 'java', '&& retained.getContext() instanceof Context);', '&& false);'),
    ('v3-send-cleanup', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'if (retained != null && retained.getContext() == context) session.removeMessageContext(context.requestId);', 'if (false) session.removeMessageContext(context.requestId);'),
    ('v3-send-owned-cleanup', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'if (retained != null && retained.getContext() == context)', 'if (retained != null)'),
    ('v3-request-cursor', 'agent/src/cph/archive/ArchiveRequests.java', 'java', 'cursor = Optional.of(new Pair<>(pairs.iterator().next().getOne(), QuerySnapshot.copy(query.getBatchLastFileId())));', 'cursor = Optional.empty();'),
    ('v3-query-snapshot', 'agent/src/cph/archive/QuerySnapshot.java', 'java', 'id.getArray().clone()', 'id.getArray()'),
    ('v3-response-cursor', 'agent/src/cph/archive/QuerySnapshot.java', 'java', 'copy(snapshot.nextCursor) : query.getBatchLastFileId()', 'query.getBatchLastFileId() : query.getBatchLastFileId()'),
    ('v3-version-pages', 'agent/src/cph/archive/VersionPages.java', 'java', 'if (response.hasBatchVersion()) {', 'if (false) {'),
    ('v3-version-cursor', 'agent/src/cph/archive/VersionPages.java', 'java', 'cursor.toByteArray(), context.query().getBatchSize()', 'null, context.query().getBatchSize()'),
    ('v3-version-repeat', 'agent/src/cph/archive/VersionPages.java', 'java', '!cursors.add(cursor)', 'false'),
    ('v3-version-progress', 'agent/src/cph/archive/VersionPages.java', 'java', 'versions.size() == before', 'false'),
    ('v3-version-conflict', 'agent/src/cph/archive/VersionPages.java', 'java', 'previous != null && !Arrays.equals(previous.toVersionBytes(), version.toVersionBytes())', 'false'),
    ('v3-version-page-limit', 'agent/src/cph/archive/VersionPages.java', 'java', '++pages > MAX_PAGES', '++pages < 0'),
    ('v3-version-byte-limit', 'agent/src/cph/archive/VersionPages.java', 'java', '(bytes += response.getSerializedSize()) > MAX_BYTES', '(bytes += response.getSerializedSize()) < 0'),
    ('contents-error-terminal', 'internal/engine/contents_stream.go', './internal/engine', 's.closed = terminal', 's.closed = true'),
    ('search-http-drain', 'internal/engine/archive_search.go', './internal/engine', 'if e != nil || matched && done {\n\t\tbackground()', 'if e != nil || matched && done {\n\t\tfinish(true)'),
    ('search-uncertainty', 'internal/engine/archive_search.go', './internal/engine', 'if !drained {', 'if false {'),
    ('search-connection-epoch', 'internal/engine/archive_search.go', './internal/engine', '\t\t\t\t\tif !c.currentGeneration(work) || !c.sessionCurrent(work) || bus.Epoch() != epoch {', '\t\t\t\t\tif !c.currentGeneration(work) || !c.sessionCurrent(work) {'),

]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=pathlib.Path)
    parser.add_argument("--only", action="append", choices=[item[0] for item in MUTATIONS], help="Run only a named mutation; can be repeated.")
    parser.add_argument("--go-image", help="Use an existing Go build image with cached modules; no network or host SDK download.")
    parser.add_argument("--vendor-lib", type=pathlib.Path, help="Pinned local vendor libraries for Java admission mutants.")
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parent.parent
    originals = {name: (root / name).read_bytes() for _, name, *_ in MUTATIONS}
    results = []
    if args.output:
        if args.output.is_symlink():
            raise ValueError("output directory must not be a symlink")
        args.output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="cph-control-mutations-") as scratch:
        for label, name, package, before, after in MUTATIONS:
            if args.only and label not in args.only:
                continue
            source = originals[name].decode()
            if source.count(before) != 1:
                results.append({"name": label, "exitCode": None, "assertionKilled": False, "failure": "AMBIGUOUS_SELECTOR"})
                continue
            destination = pathlib.Path(scratch) / label
            destination.mkdir()
            if package == "java":
                if not args.vendor_lib:
                    raise ValueError("Java mutation requires --vendor-lib")
                shutil.copytree(root / "agent", destination / "agent")
                (destination / name).write_text(source.replace(before, after, 1))
                builder = next(line.split("=", 1)[1] for line in (root / "Dockerfile").read_text().splitlines() if line.startswith("ARG TEMURIN_IMAGE="))
                java_tests = ['cph.RestoreBoundaryTest', 'cph.DirectRestoreBoundaryTest', 'cph.RestoreStrategiesTest', 'cph.RestoreRootAvailabilityTest', 'cph.RestoreLinkSerializationTest', 'cph.NativeQueryEchoTest']
                if label.startswith('v3-'):
                    java_tests = ['cph.archive.ArchiveRegistryBoundsTest', 'cph.archive.ArchiveRequestContextTest']
                    if label == 'v3-request-cursor': java_tests = ['cph.archive.ArchiveResponseContextTest']
                    if label == 'v3-response-cursor': java_tests = ['cph.archive.ArchiveChildrenPagesTest']
                    if label.startswith('v3-version-'): java_tests = ['cph.archive.ArchiveVersionPagesTest']
                compile_agent = 'mkdir /tmp/classes; javac --release 25 -Xlint:all,-path -Werror -cp "/vendor/*" -d /tmp/classes $(find /agent/src /agent/test -name "*.java"); echo CPH_MUTANT_COMPILED; printf "Premain-Class: cph.Agent\\n" >/tmp/manifest; jar cfm /tmp/agent.jar /tmp/manifest -C /tmp/classes cph; '
                run_tests = '; '.join('CPH_PATCHES=none java --add-opens=java.base/java.io=ALL-UNNAMED --add-opens=java.base/java.util=ALL-UNNAMED -Xverify:all -javaagent:/tmp/agent.jar -cp "/tmp/classes:/vendor/*" '+test for test in java_tests)
                command = ["docker", "run", "--rm", "--network", "none", "-v", str(destination / "agent") + ":/agent:ro", "-v", str(args.vendor_lib.resolve()) + ":/vendor:ro", builder, "bash", "-euc", compile_agent + run_tests]
            else:
                command = None
            for filename in (() if package == "java" else ("go.mod", "go.sum")):
                shutil.copy2(root / filename, destination / filename)
            if package != "java":
                shutil.copytree(root / "internal", destination / "internal")
                shutil.copytree(root / "cmd", destination / "cmd")
                (destination / name).write_text(source.replace(before, after, 1))
                command = ["go", "test", package, "-count=1", "-timeout=10s"]
                if label == "discovery-owner-queue":
                    command += ["-run", "TestPublicConfigurationWaitsForTheOwner"]
            if args.go_image and package != "java":
                command = ["docker", "run", "--rm", "--network", "none", "-e", "GOTOOLCHAIN=local", "-v", str(destination) + ":/work:rw", "-w", "/work", "--entrypoint", "go", args.go_image, *command[1:]]
            try:
                process = subprocess.run(
                    command,
                    cwd=destination, capture_output=True, text=True, timeout=30,
                    env={**os.environ, "GOTOOLCHAIN": "local"},
                )
                output = process.stdout + process.stderr
                killed = process.returncode != 0 and (("CPH_MUTANT_COMPILED" in output and "AssertionError" in output) if package == "java" else ("--- FAIL:" in output and "[build failed]" not in output))
                result = {"name": label, "exitCode": process.returncode, "assertionKilled": killed}
            except subprocess.TimeoutExpired:
                output = "Mutation runner deadline exceeded. This is not an assertion kill.\n"
                result = {"name": label, "exitCode": None, "assertionKilled": False}
            results.append(result)
            if args.output:
                target = args.output / (label + ".txt")
                if target.is_symlink():
                    raise ValueError("output file must not be a symlink")
                target.write_text(output)
    restored = all((root / name).read_bytes() == content for name, content in originals.items())
    report = {
        "sourceHashes": {name: hashlib.sha256(value).hexdigest() for name, value in originals.items()},
        "mutations": results, "originalsUnchanged": restored,
        "scope": "Named control and storage guards, not exhaustive syscall fault injection.",
    }
    if args.output:
        target = args.output / "report.json"
        if target.is_symlink():
            raise ValueError("output file must not be a symlink")
        target.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report))
    return 0 if restored and all(item["assertionKilled"] for item in results) else 1


if __name__ == "__main__":
    raise SystemExit(main())
