// Package destinations owns admitted archive and device operations.
package destinations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/restore"
	"github.com/traktuner/crashplan-headless/internal/statefile"
)

var ErrJournal = errors.New("LOCAL_ARCHIVE_JOURNAL_UNAVAILABLE")
var requestID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Service struct {
	mu     sync.Mutex
	native *engine.Client
	roots  *restore.Destinations
	store  *statefile.Store
}

func NewService(native *engine.Client, roots *restore.Destinations, store *statefile.Store) (*Service, error) {
	if native == nil || roots == nil || store == nil {
		return nil, ErrJournal
	}
	return &Service{native: native, roots: roots, store: store}, nil
}

type Request struct {
	RequestID     string         `json:"requestId"`
	Action        string         `json:"action"`
	Target        restore.Target `json:"target"`
	SourceID      string         `json:"sourceId,omitempty"`
	DestinationID string         `json:"destinationId,omitempty"`
	Confirm       string         `json:"confirm"`
	DeviceID      string         `json:"deviceId,omitempty"`
	Name          string         `json:"name,omitempty"`
}
type Operation struct {
	engine.LocalArchiveResult
	RequestID string `json:"requestId"`
	Digest    string `json:"requestDigest"`
}

func (s *Service) Start(ctx context.Context, in Request) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deviceAction := in.Action == "rename" || in.Action == "remove" || in.Action == "maintenance"
	confirm := strings.ToUpper(in.Action)
	if deviceAction {
		confirm += " " + in.DeviceID
	}
	if !requestID.MatchString(in.RequestID) || (in.Action != "create" && in.Action != "attach" && in.Action != "move" && in.Action != "relocate" && !deviceAction) || in.Confirm != confirm {
		return Operation{}, engine.ErrInvalidRequest
	}
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	name := "local-archive-" + in.RequestID + ".json"
	if raw, err := s.store.Read(name); err == nil {
		var saved Operation
		if json.Unmarshal(raw, &saved) != nil || saved.RequestID != in.RequestID || saved.Digest != digest {
			return saved, ErrJournal
		}
		switch saved.State {
		case "indeterminate", "accepted", "completed", "needs-attach", "source-missing", "relocated", "rejected":
			return saved, nil
		default:
			return saved, ErrJournal
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Operation{}, ErrJournal
	}
	if deviceAction {
		if in.SourceID != "" || in.DestinationID != "" || in.Target.RootID != "" || in.Target.Relative != "" {
			return Operation{}, engine.ErrInvalidRequest
		}
		return s.deviceOperation(ctx, in, digest, name)
	}
	if in.DeviceID != "" || in.Name != "" {
		return Operation{}, engine.ErrInvalidRequest
	}
	target, err := s.roots.Resolve(in.Target.RootID, in.Target.Relative)
	if err != nil {
		return Operation{}, err
	}
	defer target.Close()
	pinned := []*restore.Destination{target}
	var children []*restore.DirectoryCandidate
	var missing []*restore.DirectoryCandidate
	defer func() {
		for _, child := range children {
			child.Close()
		}
	}()
	defer func() {
		for _, p := range pinned[1:] {
			p.Close()
		}
	}()
	validate := func(path string) error {
		for _, child := range missing {
			if child.RequireMissing() != nil {
				return restore.ErrDestination
			}
		}
		for _, child := range children {
			if child.Validate() != nil {
				return restore.ErrDestination
			}
		}
		for _, p := range pinned {
			if p.Validate() != nil || !p.Writable() {
				return restore.ErrDestination
			}
		}
		for _, p := range pinned {
			if p.Path() == path {
				return nil
			}
		}
		d, err := s.roots.ResolvePath(path)
		if err != nil {
			return err
		}
		pinned = append(pinned, d)
		if d.Validate() != nil || !d.Writable() {
			return restore.ErrDestination
		}
		return nil
	}
	if validate(target.Path()) != nil {
		return Operation{}, restore.ErrDestination
	}
	policy, err := s.native.LocalArchivePolicy(ctx)
	if err != nil {
		return Operation{}, err
	}
	source := in.SourceID
	if source == "" {
		source = policy.SelfID
	}
	var sourceDevice, targetDevice *engine.LocalArchiveDevice
	for i := range policy.Devices {
		d := &policy.Devices[i]
		if d.ID == source {
			sourceDevice = d
		}
		if d.ID == in.DestinationID {
			targetDevice = d
		}
	}
	if sourceDevice == nil {
		return Operation{}, engine.ErrInvalidRequest
	}
	ownedAllowed := policy.OwnDevices || (in.Action == "attach" && policy.CanAttachOwn)
	if (sourceDevice.Self && !policy.Allowed) || (!sourceDevice.Self && (!sourceDevice.Own || !ownedAllowed)) {
		return Operation{}, engine.ErrSettingLocked
	}
	if in.Action == "create" && (!sourceDevice.Self || in.DestinationID != "") {
		return Operation{}, engine.ErrInvalidRequest
	}
	if in.DestinationID != "" && (targetDevice == nil || (sourceDevice.Self && targetDevice.ParentID != policy.SelfID) || (!sourceDevice.Self && !targetDevice.Self)) {
		return Operation{}, engine.ErrInvalidRequest
	}
	child, err := target.ChildDirectory(source)
	if err != nil {
		return Operation{}, err
	}
	children = append(children, child)
	var oldPath string
	var locationDevice *engine.LocalArchiveDevice
	if in.Action == "move" || in.Action == "relocate" {
		if targetDevice == nil {
			return Operation{}, engine.ErrInvalidRequest
		}
		device := sourceDevice
		if sourceDevice.Self {
			device = targetDevice
		}
		locationDevice = device
		path, err := s.native.LocalArchivePath(ctx, *device)
		if err != nil {
			return Operation{}, err
		}
		oldPath = path
		if in.Action == "relocate" {
			parent, err := s.roots.ResolvePath(path)
			if err != nil {
				return Operation{}, err
			}
			pinned = append(pinned, parent)
			absent, err := parent.ChildDirectory(source)
			if err != nil {
				return Operation{}, err
			}
			children = append(children, absent)
			if absent.RequireMissing() != nil || child.RequireMissing() != nil {
				return Operation{}, restore.ErrDestination
			}
			missing = append(missing, absent, child)
		} else if validate(filepath.Join(path, source)) != nil {
			return Operation{}, restore.ErrDestination
		}
	}
	if validate(target.Path()) != nil {
		return Operation{}, restore.ErrDestination
	}
	return s.recorded(in.RequestID, digest, name, func() (engine.LocalArchiveResult, error) {
		var result engine.LocalArchiveResult
		if !sourceDevice.Self && !policy.OwnDevices {
			current, checkErr := s.native.LocalArchivePolicy(ctx)
			owned := false
			for _, device := range current.Devices {
				if device.ID == source && device.Own {
					owned = true
				}
			}
			if checkErr != nil || current.SelfID != policy.SelfID || current.UserID != policy.UserID || !current.CanAttachOwn || !owned {
				return result, errors.Join(engine.ErrNotDispatched, engine.ErrSettingLocked, checkErr)
			}
		}
		if in.Action == "relocate" {
			result, err = s.native.RelocateMissingArchive(ctx, *locationDevice, target.Path(), oldPath, validate)
		} else {
			result, err = s.native.ExecuteLocalArchive(ctx, in.Action, target.Path(), source, in.DestinationID, validate)
		}
		if err == nil && validate(target.Path()) != nil {
			err = engine.ErrIndeterminate
		}
		return result, err
	})
}

func (s *Service) recorded(id, digest, name string, execute func() (engine.LocalArchiveResult, error)) (Operation, error) {
	op := Operation{LocalArchiveResult: engine.LocalArchiveResult{State: "indeterminate"}, RequestID: id, Digest: digest}
	save := func() error {
		raw, err := json.Marshal(op)
		if err != nil || s.store.Write(name, raw) != nil {
			return ErrJournal
		}
		return nil
	}
	if err := save(); err != nil {
		return op, err
	}
	result, err := execute()
	if err == nil {
		op.LocalArchiveResult = result
	} else if errors.Is(err, engine.ErrUnauthorized) || errors.Is(err, engine.ErrNotDispatched) {
		op.State = "rejected"
	}
	if saveErr := save(); saveErr != nil {
		return op, errors.Join(engine.ErrIndeterminate, saveErr)
	}
	return op, err
}

func (s *Service) Status(id string) (Operation, error) {
	if !requestID.MatchString(id) {
		return Operation{}, engine.ErrInvalidRequest
	}
	raw, err := s.store.Read("local-archive-" + id + ".json")
	if err != nil {
		return Operation{}, ErrJournal
	}
	var saved Operation
	if json.Unmarshal(raw, &saved) != nil || saved.RequestID != id || len(saved.Digest) != 64 {
		return saved, ErrJournal
	}
	switch saved.State {
	case "indeterminate", "accepted", "completed", "needs-attach", "source-missing", "relocated", "rejected":
		return saved, nil
	default:
		return saved, ErrJournal
	}
}
