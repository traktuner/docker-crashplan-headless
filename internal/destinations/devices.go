package destinations

import (
	"context"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/restore"
	"strings"
	"unicode/utf8"
)

func (s *Service) deviceOperation(ctx context.Context, in Request, digest, recordName string) (Operation, error) {
	if in.Action == "rename" && (strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 30 || strings.ContainsAny(in.Name, "\x00\r\n")) {
		return Operation{}, engine.ErrInvalidRequest
	}
	if in.Action != "rename" && in.Name != "" {
		return Operation{}, engine.ErrInvalidRequest
	}
	policy, err := s.native.LocalArchivePolicy(ctx)
	if err != nil {
		return Operation{}, err
	}
	if in.Action == "maintenance" {
		if in.DeviceID == policy.SelfID {
			return Operation{}, engine.ErrInvalidRequest
		}
		sets, err := s.native.BackupSets(ctx)
		if err != nil {
			return Operation{}, err
		}
		found := false
		for _, set := range sets {
			for _, d := range set.Destinations {
				if d.ID == in.DeviceID {
					found = true
				}
			}
		}
		if !found {
			return Operation{}, engine.ErrInvalidRequest
		}
		if err := s.native.CheckMaintenance(ctx, policy.SelfID, in.DeviceID); err != nil {
			return Operation{}, err
		}
		return s.recorded(in.RequestID, digest, recordName, func() (engine.LocalArchiveResult, error) {
			err := s.native.MaintainArchive(ctx, policy.SelfID, in.DeviceID)
			return engine.LocalArchiveResult{State: "accepted", DestinationID: in.DeviceID}, err
		})
	}
	var device *engine.LocalArchiveDevice
	for i := range policy.Devices {
		if policy.Devices[i].ID == in.DeviceID {
			device = &policy.Devices[i]
			break
		}
	}
	if device == nil || device.Deleted || (!device.Self && !device.Own) {
		return Operation{}, engine.ErrInvalidRequest
	}
	if in.Action == "remove" && (device.Self || device.ParentID != policy.SelfID) {
		return Operation{}, engine.ErrInvalidRequest
	}
	var parent *restore.Destination
	var child *restore.DirectoryCandidate
	if in.Action == "remove" {
		sets, err := s.native.BackupSets(ctx)
		if err != nil {
			return Operation{}, err
		}
		for _, set := range sets {
			for _, destination := range set.Destinations {
				if destination.ID == device.ID && (set.Locked || set.DestinationsLocked || destination.Locked) {
					return Operation{}, engine.ErrSettingLocked
				}
			}
		}
		path, err := s.native.LocalArchivePath(ctx, *device)
		if err != nil {
			return Operation{}, err
		}
		parent, err = s.roots.ResolvePath(path)
		if err != nil {
			return Operation{}, err
		}
		defer parent.Close()
		child, err = parent.ChildDirectory(policy.SelfID)
		if err != nil {
			return Operation{}, err
		}
		defer child.Close()
	}
	before := func() error {
		if parent != nil && (parent.Validate() != nil || !parent.Writable() || child.Validate() != nil) {
			return restore.ErrDestination
		}
		return nil
	}
	if err := before(); err != nil {
		return Operation{}, err
	}
	return s.recorded(in.RequestID, digest, recordName, func() (engine.LocalArchiveResult, error) {
		result, err := s.native.ChangeDevice(ctx, in.DeviceID, in.Action, in.Name, before)
		// Removal may delete the archive child. Its admitted parent must remain.
		if err == nil && parent != nil && (parent.Validate() != nil || !parent.Writable()) {
			return result, engine.ErrIndeterminate
		}
		return result, err
	})
}
