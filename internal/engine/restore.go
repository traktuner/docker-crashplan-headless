package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrArchiveKey            = errors.New("ARCHIVE_UNLOCK_REQUIRED")
	ErrRestoreMarker         = errors.New("RESTORE_MARKER_REJECTED")
	ErrRestorePath           = errors.New("RESTORE_DESTINATION_REJECTED")
	ErrRestoreSpace          = errors.New("RESTORE_NO_SPACE")
	ErrRestoreReadOnly       = errors.New("RESTORE_DESTINATION_READ_ONLY")
	ErrRestoreDuplicateNames = errors.New("RESTORE_DUPLICATE_NAMES")
)

// Credentials never serialize or format native key material into product output.
type RestoreCredentials struct {
	userID                   int
	key                      string
	helperRequired, verified bool
	generation               any
	revision                 uint64
}

func (RestoreCredentials) String() string         { return "engine.RestoreCredentials{private}" }
func (RestoreCredentials) GoString() string       { return "engine.RestoreCredentials{private}" }
func (c RestoreCredentials) UserID() int          { return c.userID }
func (c RestoreCredentials) RequiresMarker() bool { return c.helperRequired }

func (c *Client) RestoreContext(ctx context.Context, source string) (RestoreCredentials, error) {
	ctx = c.operationContext(ctx)
	if !validID(source) {
		return RestoreCredentials{}, ErrInvalidRequest
	}
	raw, err := c.legacyRead(ctx, "StatusResponseMessage", "POST", "/v0/StatusQuery", statusQuery(c.home()), anyAnswer)
	raw, err = restoreResponse(raw, err, "StatusResponseMessage")
	if err != nil {
		return RestoreCredentials{}, err
	}
	var account struct {
		User struct {
			ID *int `json:"userId"`
		} `json:"user"`
	}
	if json.Unmarshal(raw, &account) != nil || account.User.ID == nil || *account.User.ID < 0 || int64(*account.User.ID) > 2147483647 {
		return RestoreCredentials{}, ErrProtocol
	}
	body, _ := json.Marshal(struct {
		Sources []string `json:"sourceGuids"`
	}{[]string{source}})
	raw, err = c.legacyRead(ctx, "RestorePrivateKeyResponse", "POST", "/v0/RestorePrivateKeyRequest", body, func(decoded []byte) probeResult {
		var echo struct {
			Items []struct {
				Source json.Number `json:"sourceGuid"`
			} `json:"securityKeyInfoList"`
		}
		if json.Unmarshal(decoded, &echo) == nil && len(echo.Items) == 1 && echo.Items[0].Source.String() == source {
			return probeInStep
		}
		return probeMismatch
	})
	raw, err = restoreResponse(raw, err, "RestorePrivateKeyResponse")
	if err != nil {
		return RestoreCredentials{}, err
	}
	var keys struct {
		Items []struct {
			Source json.Number `json:"sourceGuid"`
			Key    *struct {
				Array string `json:"array"`
			} `json:"dataKey"`
		} `json:"securityKeyInfoList"`
	}
	if json.Unmarshal(raw, &keys) != nil || len(keys.Items) != 1 || keys.Items[0].Source.String() != source {
		return RestoreCredentials{}, ErrProtocol
	}
	key := c.archiveKey(source)
	if key == "" && keys.Items[0].Key != nil {
		key = keys.Items[0].Key.Array
	}
	if key == "" {
		return RestoreCredentials{}, ErrArchiveKey
	}
	if len(key) > 65536 {
		return RestoreCredentials{}, ErrProtocol
	}
	if _, err := base64.StdEncoding.Strict().DecodeString(key); err != nil {
		return RestoreCredentials{}, ErrProtocol
	}
	helperRequired := true
	const setting = "device_engine_writepermission_enforcement_enabled"
	flag, flagErr := c.request(ctx, "GET", "/v1/Setting/"+setting, nil, true, false)
	if flagErr != nil {
		var status *nativeStatusError
		// SettingGet uses a StringSetting without a default. Its404 means absent.
		// RestoreToolService uses the same setting with the Boolean default true.
		if !errors.As(flagErr, &status) || status.status != 404 {
			return RestoreCredentials{}, flagErr
		}
	} else {
		var values map[string]string
		if json.Unmarshal(flag, &values) != nil {
			return RestoreCredentials{}, ErrProtocol
		}
		value, ok := values[setting]
		if !ok {
			return RestoreCredentials{}, ErrProtocol
		}
		switch strings.ToLower(value) {
		case "true":
		case "false":
			helperRequired = false
		default:
			return RestoreCredentials{}, ErrProtocol
		}
	}
	return RestoreCredentials{userID: *account.User.ID, key: key, helperRequired: helperRequired, verified: true, generation: ctx.Value(generationKey{}), revision: c.boundRevision(ctx)}, nil
}

func restoreResponse(raw []byte, requestErr error, expected string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, requestErr
	}
	decoded, ids, err := responseEnvelope(raw, expected)
	if err != nil {
		if requestErr != nil {
			return nil, requestErr
		}
		return nil, err
	}
	for _, id := range ids {
		switch id {
		case "PERMISSION_DENIED":
			return nil, ErrRestoreMarker
		case "PATH_DOESNT_EXIST":
			return nil, ErrRestorePath
		case "NO_SPACE":
			return nil, ErrRestoreSpace
		case "READ_ONLY":
			return nil, ErrRestoreReadOnly
		}
	}
	if len(ids) > 0 {
		return nil, ErrRejected
	}
	if requestErr != nil {
		return nil, requestErr
	}
	return decoded, nil
}

type RestoreCheck struct {
	ID     string
	Marker string
}

var markerPattern = regexp.MustCompile(`^/tmp/[0-9]{1,20}\.txt$`)

func (c *Client) CheckRestore(ctx context.Context) (RestoreCheck, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.legacyRequest(ctx, "RestoreCheckResponseMessage", "POST", "/v0/RestoreCheckRequest", []byte(`{}`), true, true)
	raw, err = restoreResponse(raw, err, "RestoreCheckResponseMessage")
	if err != nil {
		return RestoreCheck{}, err
	}
	var wire struct {
		ID   json.Number `json:"restoreId"`
		File string      `json:"fileName"`
	}
	if json.Unmarshal(raw, &wire) != nil || !validID(wire.ID.String()) || !markerPattern.MatchString(wire.File) {
		return RestoreCheck{}, errors.Join(ErrIndeterminate, ErrProtocol)
	}
	return RestoreCheck{wire.ID.String(), wire.File}, nil
}

type RestoreSelection struct {
	Path        string `json:"path"`
	BackupSetID string `json:"backupSetId"`
	Timestamp   string `json:"timestamp"`
	IsFolder    bool   `json:"isFolder"`
}
type RestoreSubmission struct {
	ID             string             `json:"-"`
	SourceID       string             `json:"sourceId"`
	TargetID       string             `json:"targetId"`
	Destination    string             `json:"destination"`
	Existing       string             `json:"existing"`
	Permissions    string             `json:"permissions"`
	IncludeDeleted bool               `json:"includeDeleted"`
	Items          []RestoreSelection `json:"items"`
	Excluded       []RestoreSelection `json:"excluded,omitempty"`
}

func validTimestamp(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == value
}
func (in RestoreSubmission) Validate() error {
	if !validID(in.SourceID) || !validID(in.TargetID) || !filepath.IsAbs(in.Destination) || filepath.Clean(in.Destination) != in.Destination || in.Destination == "/" || strings.ContainsAny(in.Destination, "\x00\r\n") || len(in.Destination) > 65536 || len(in.Items) == 0 || len(in.Items) > 10000 {
		return ErrInvalidRequest
	}
	switch in.Existing {
	case "OVERWRITE_ORIGINAL", "RENAME_ORIGINAL", "SKIP_IF_NEWER", "SKIP_IF_OLDER", "SKIP_ALL":
	default:
		return ErrInvalidRequest
	}
	if in.Permissions != "current" && in.Permissions != "original" {
		return ErrInvalidRequest
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, item := range in.Items {
		if item.Path == "" || len(item.Path) > 65536 || strings.ContainsAny(item.Path, "\x00\r\n") || seen[item.Path] || !validID(item.BackupSetID) || !validTimestamp(item.Timestamp) {
			return ErrInvalidRequest
		}
		seen[item.Path] = true
		name := filepath.Base(strings.TrimRight(item.Path, "/"))
		if names[name] {
			return errors.Join(ErrInvalidRequest, ErrRestoreDuplicateNames)
		}
		names[name] = true
	}
	if len(in.Excluded) > 10000 {
		return ErrInvalidRequest
	}
	for _, item := range in.Excluded {
		if item.Path == "" || len(item.Path) > 65536 || strings.ContainsAny(item.Path, "\x00\r\n") || seen[item.Path] || !validID(item.BackupSetID) || !validTimestamp(item.Timestamp) {
			return ErrInvalidRequest
		}
		contained := false
		for _, parent := range in.Items {
			if parent.IsFolder && parent.BackupSetID == item.BackupSetID && strings.HasPrefix(item.Path, strings.TrimRight(parent.Path, "/")+"/") {
				contained = true
				break
			}
		}
		if !contained {
			return ErrInvalidRequest
		}
		seen[item.Path] = true
	}
	return nil
}
func (c *Client) AddRestore(ctx context.Context, credentials RestoreCredentials, in RestoreSubmission) (string, error) {
	ctx = c.operationContext(ctx)
	if in.Validate() != nil || !credentials.verified || credentials.key == "" || credentials.userID < 0 {
		return "", ErrInvalidRequest
	}
	// Both refusals happen before the native request. Report them as not
	// dispatched so the submission record does not stay unresolved.
	if credentials.revision != c.SessionRevision() {
		return "", errors.Join(ErrNotDispatched, ErrSessionChanged)
	}
	if credentials.generation != nil && ctx.Value(generationKey{}) != credentials.generation {
		return "", errors.Join(ErrNotDispatched, ErrUnavailable)
	}
	if credentials.helperRequired {
		if !validID(in.ID) {
			return "", ErrInvalidRequest
		}
	} else {
		if in.ID == "" {
			in.ID = "0"
		}
		if in.ID != "0" {
			return "", ErrInvalidRequest
		}
	}
	type selection struct {
		Set       string `json:"backupSetId"`
		Timestamp string `json:"timestamp"`
		Folder    bool   `json:"isFolder"`
		Selected  bool   `json:"selected"`
	}
	files := make(map[string]selection, len(in.Items))
	for _, item := range in.Items {
		files[item.Path] = selection{item.BackupSetID, item.Timestamp, item.IsFolder, true}
	}
	for _, item := range in.Excluded {
		files[item.Path] = selection{item.BackupSetID, item.Timestamp, item.IsFolder, false}
	}
	body, err := json.Marshal(struct {
		ID             string               `json:"restoreId"`
		UserID         int                  `json:"userId"`
		Source         string               `json:"sourceId"`
		Target         string               `json:"targetId"`
		IncludeDeleted bool                 `json:"includeDeleted"`
		NumFiles       int                  `json:"numFilesToRestore"`
		NumBytes       string               `json:"numBytesToRestore"`
		UID            int                  `json:"osUid"`
		GID            int                  `json:"osGid"`
		UseUID         bool                 `json:"useOsUids"`
		Key            string               `json:"dataKey,omitempty"`
		Path           string               `json:"restorePath"`
		Existing       string               `json:"existingFilesRestoreStrategy"`
		Subtype        string               `json:"clientRestoreSubType"`
		Files          map[string]selection `json:"files"`
	}{in.ID, credentials.userID, in.SourceID, in.TargetID, in.IncludeDeleted, 0, "0", os.Geteuid(), os.Getegid(), in.Permissions == "current", credentials.key, in.Destination, in.Existing, "STANDARD", files})
	if err != nil {
		return "", ErrInvalidRequest
	}
	raw, err := c.request(ctx, "POST", "/v1/AddRestore", body, true, true)
	raw, err = restoreResponse(raw, err, "AddRestoreResponseMessage")
	if err != nil {
		return "", err
	}
	var wire struct {
		ID json.Number `json:"restoreId"`
	}
	if json.Unmarshal(raw, &wire) != nil || !validID(wire.ID.String()) || (credentials.helperRequired && wire.ID.String() != in.ID) {
		return "", errors.Join(ErrIndeterminate, ErrProtocol)
	}
	return wire.ID.String(), nil
}

type RestoreJob struct {
	ID                          string `json:"id"`
	SourceID                    string `json:"sourceId"`
	TargetID                    string `json:"targetId"`
	Completed                   bool   `json:"completed"`
	Canceled                    bool   `json:"canceled"`
	NumFiles                    string `json:"numFiles"`
	FilesRestored               string `json:"filesRestored"`
	NumBytes                    string `json:"numBytes"`
	BytesRestored               string `json:"bytesRestored"`
	destination, userName, kind string
	hasUser                     bool
}

func (j RestoreJob) Destination() string { return j.destination }
func (j RestoreJob) UserName() string    { return j.userName }
func (j RestoreJob) UsesHelper() bool    { return j.kind == "CLIENT" && j.hasUser }
func (c *Client) RestoreJobs(ctx context.Context) ([]RestoreJob, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.legacyRead(ctx, "RestoreJobsResponseMessage", "PUT", "/v0/RestoreJobsRequest", []byte(`{}`), func([]byte) probeResult {
		// ControlRestore answers with the same class; a list read must not
		// clear an uncertainty that a restore control left behind.
		return probeUnknown
	})
	raw, err = restoreResponse(raw, err, "RestoreJobsResponseMessage")
	if err != nil {
		return nil, err
	}
	var wire struct {
		Jobs *[]struct {
			ID            json.Number `json:"restoreId"`
			Source        json.Number `json:"sourceId"`
			Target        json.Number `json:"targetId"`
			Completed     *bool       `json:"completed"`
			Canceled      *bool       `json:"canceled"`
			Path          *string     `json:"restorePath"`
			User          *string     `json:"userName"`
			Kind          string      `json:"restoreType"`
			NumFiles      json.Number `json:"numFilesToRestore"`
			FilesRestored json.Number `json:"numFilesRestored"`
			NumBytes      json.Number `json:"numBytesToRestore"`
			BytesRestored json.Number `json:"numBytesRestored"`
		} `json:"jobs"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Jobs == nil || len(*wire.Jobs) > 10000 {
		return nil, ErrProtocol
	}
	result := make([]RestoreJob, 0, len(*wire.Jobs))
	seen := map[string]bool{}
	for _, item := range *wire.Jobs {
		id := item.ID.String()
		if !validID(id) || seen[id] || !validID(item.Source.String()) || !validID(item.Target.String()) || item.Completed == nil || item.Canceled == nil {
			return nil, ErrProtocol
		}
		seen[id] = true
		switch item.Kind {
		case "CLIENT", "PUSH", "WEB", "ADVANCED":
		default:
			return nil, ErrProtocol
		}
		job := RestoreJob{ID: id, SourceID: item.Source.String(), TargetID: item.Target.String(), Completed: *item.Completed, Canceled: *item.Canceled, kind: item.Kind}
		if item.Path != nil {
			job.destination = *item.Path
		}
		if item.User != nil {
			job.userName = *item.User
			job.hasUser = true
		}
		for _, counter := range []struct {
			number json.Number
			out    *string
		}{{item.NumFiles, &job.NumFiles}, {item.FilesRestored, &job.FilesRestored}, {item.NumBytes, &job.NumBytes}, {item.BytesRestored, &job.BytesRestored}} {
			n, e := counter.number.Int64()
			if e != nil || n < 0 {
				return nil, ErrProtocol
			}
			*counter.out = counter.number.String()
		}
		result = append(result, job)
	}
	return result, nil
}

func (c *Client) ControlRestore(ctx context.Context, id, action string) error {
	ctx = c.operationContext(ctx)
	if !validID(id) {
		return ErrInvalidRequest
	}
	switch action {
	case "START", "STOP", "REMOVE":
	default:
		return ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(c.generationContext(ctx), 10*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"restoreId": id, "action": action})
	raw, err := c.legacyRequest(ctx, "RestoreJobsResponseMessage", "PUT", "/v0/ControlRestore", body, true, true)
	raw, err = restoreResponse(raw, err, "RestoreJobsResponseMessage")
	if err != nil {
		return err
	}
	var reply struct {
		Jobs *[]json.RawMessage `json:"jobs"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.Jobs == nil {
		return ErrIndeterminate
	}
	return nil
}

// Native legacy catchers correlate by response class rather than request ID.
// Keep same-class calls serial, retaining uncertainty after a lost response.
// A new JVM generation cannot retain the old JVM's response catchers/workers.
type legacyLane struct {
	gate      chan struct{}
	uncertain bool
}

func (c *Client) legacyRequest(ctx context.Context, class, method, path string, body []byte, auth, mutation bool) ([]byte, error) {
	c.mu.Lock()
	if ctx.Value(generationKey{}) == nil && c.lifetime != nil {
		ctx = context.WithValue(ctx, generationKey{}, c.lifetime.Value(generationKey{}))
	}
	if c.legacy == nil {
		c.legacy = make(map[string]*legacyLane)
	}
	lane := c.legacy[class]
	if lane == nil {
		lane = &legacyLane{gate: make(chan struct{}, 1)}
		c.legacy[class] = lane
	}
	c.mu.Unlock()
	select {
	case lane.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
	defer func() { <-lane.gate }()
	if !c.currentGeneration(ctx) || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	c.mu.RLock()
	uncertain := lane.uncertain
	c.mu.RUnlock()
	if uncertain {
		return nil, ErrLegacyPending
	}
	raw, err := c.request(ctx, method, path, body, auth, mutation)
	if err == nil && !c.currentGeneration(ctx) {
		err = ErrUnavailable
	}
	if err != nil && !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotDispatched) {
		c.mu.Lock()
		lane.uncertain = true
		c.mu.Unlock()
	}
	return raw, err
}

func statusQuery(home string) []byte {
	body, _ := json.Marshal(map[string]string{"appCode": "CPH", "authToken": "", "userHome": home, "desktopFolder": home})
	return body
}
