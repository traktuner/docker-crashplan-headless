package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrSearchPending = errors.New("ARCHIVE_SEARCH_PENDING")
var ErrSearchScope = errors.New("ARCHIVE_SEARCH_SCOPE_UNSUPPORTED")

const searchClass = "SearchFileVersionsResultMessage"

// searchUncertainTTL bounds how long a search without a terminal event blocks
// the next one. The drain already waited 30 s; enriched archives echo pattern
// and GUID, so a late foreign result is rejected, and V1 results still have
// to match the complete query identity.
const searchUncertainTTL = 10 * time.Minute

type ArchiveSearch struct {
	ArchiveQuery
	Pattern  string `json:"pattern"`
	FullPath bool   `json:"fullPath"`
}

// Search owns its response class until ordered WebSocket drain, including when
// the HTTP caller already received the result or an error.
func (c *Client) SearchArchive(ctx context.Context, credentials RestoreCredentials, in ArchiveSearch) (ArchivePage, error) {
	ctx = c.operationContext(ctx)
	q, err := in.ArchiveQuery.normalized()
	if err != nil || q.Cursor != "" || strings.TrimSpace(in.Pattern) == "" || len(in.Pattern) > 4096 || strings.ContainsAny(in.Pattern, "\x00\r\n") {
		return ArchivePage{}, ErrInvalidRequest
	}
	if !credentials.verified || credentials.key == "" {
		return ArchivePage{}, ErrArchiveKey
	}
	if credentials.revision != c.SessionRevision() {
		return ArchivePage{}, ErrSessionChanged
	}
	if credentials.generation != nil && credentials.generation != ctx.Value(generationKey{}) {
		return ArchivePage{}, ErrUnavailable
	}
	if q.FileID != archiveRoot {
		devices, err := c.Devices(ctx)
		if err != nil {
			return ArchivePage{}, err
		}
		knownV1 := false
		for _, device := range devices {
			if device.ID != q.TargetID {
				continue
			}
			if device.ArchiveType == "ARCHIVE_V3" {
				return ArchivePage{}, ErrSearchScope
			}
			knownV1 = device.ArchiveType == "ARCHIVE_V1"
		}
		if !knownV1 {
			return ArchivePage{}, ErrProtocol
		}
	}
	if !c.searchMu.TryLock() {
		return ArchivePage{}, ErrSearchPending
	}
	uncertain := c.readLaneUncertain(searchClass)
	c.mu.RLock()
	life := c.lifetime
	c.mu.RUnlock()
	if uncertain {
		c.searchMu.Unlock()
		return ArchivePage{}, ErrSearchPending
	}
	if life == nil {
		life = context.Background()
	}
	if marker := ctx.Value(generationKey{}); marker != nil {
		life = context.WithValue(life, generationKey{}, marker)
	}
	life = context.WithValue(life, sessionKey{}, ctx.Value(sessionKey{}))
	work, cancel := context.WithTimeout(life, 30*time.Second)
	bus := c.EventBus()
	events, unsubscribe, err := bus.Subscribe(searchClass)
	if err != nil {
		cancel()
		c.searchMu.Unlock()
		return ArchivePage{}, err
	}
	finish := func(drained bool) {
		if !drained {
			c.markReadUncertain(work, searchClass, causeDrain, searchUncertainTTL)
		}
		cancel()
		unsubscribe()
		c.searchMu.Unlock()
	}
	if _, err := c.OpenEvents(life); err != nil {
		finish(true)
		return ArchivePage{}, err
	}
	epoch := bus.Epoch()
	if ctx.Err() != nil {
		finish(true)
		return ArchivePage{}, ErrUnavailable
	}
	// Native V1 has no pattern echo. Enriched V3 echoes pattern, mode and GUID
	// after the native search context verifies its own request identity.
	guid := ""
	consume := func(raw []byte) (ArchivePage, error, bool, bool) {
		matched, e := matchArchiveQuery(q, raw)
		if !matched {
			return ArchivePage{}, e, false, false
		}
		var wire struct {
			Done  *bool `json:"done"`
			Query struct {
				Pattern *string     `json:"cphSearchPattern"`
				Full    *bool       `json:"cphSearchFullPath"`
				Guid    json.Number `json:"cphSearchGuid"`
			} `json:"backupQueryData"`
		}
		if json.Unmarshal(raw, &wire) != nil || wire.Done == nil {
			return ArchivePage{}, ErrProtocol, false, true
		}
		if wire.Query.Pattern != nil || wire.Query.Full != nil || wire.Query.Guid != "" {
			if wire.Query.Pattern == nil || wire.Query.Full == nil || wire.Query.Guid == "" || *wire.Query.Pattern != in.Pattern || *wire.Query.Full != in.FullPath {
				return ArchivePage{}, ErrProtocol, false, false
			}
			if guid != "" && guid != wire.Query.Guid.String() {
				return ArchivePage{}, nil, false, false
			}
			guid = wire.Query.Guid.String()
		}
		if e != nil {
			return ArchivePage{}, e, *wire.Done, true
		}
		page, e := decodeArchivePage(q, raw)
		if e != nil {
			return ArchivePage{}, e, false, true
		}
		page.Incomplete = len(page.Items) >= q.Limit
		page.NextCursor = ""
		return page, nil, *wire.Done, true
	}
	// Transfer the subscription and mutex to a bounded drain when HTTP can return.
	background := func() {
		go func() {
			drained := false
			defer func() { finish(drained) }()
			for {
				select {
				case <-work.Done():
					return
				case <-bus.Done():
					return
				case raw := <-events:
					if !c.currentGeneration(work) || !c.sessionCurrent(work) || bus.Epoch() != epoch {
						return
					}
					_, _, done, matched := consume(raw)
					if matched && done {
						drained = true
						return
					}
				}
			}
		}()
	}
	var request map[string]any
	if json.Unmarshal(q.body(""), &request) != nil {
		finish(true)
		return ArchivePage{}, ErrInvalidRequest
	}
	request["searchRegex"] = in.Pattern
	request["searchFullPath"] = in.FullPath
	request["privateKey"] = credentials.key
	body, _ := json.Marshal(request)
	raw, requestErr := c.request(ctx, "PUT", "/v0/SearchFileVersions", body, true, false)
	if errors.Is(requestErr, ErrNotDispatched) || errors.Is(requestErr, ErrUnauthorized) {
		finish(true)
		return ArchivePage{}, requestErr
	}
	decoded, decodeErr := restoreResponse(raw, requestErr, searchClass)
	if decodeErr != nil {
		if requestErr != nil && receivedRejection(raw, searchClass) {
			// The native handler answered with error ids: the search ended
			// and no result stream follows.
			finish(true)
			return ArchivePage{}, decodeErr
		}
		background()
		return ArchivePage{}, decodeErr
	}
	page, e, done, matched := consume(decoded)
	if e != nil || matched && done {
		background()
		return page, e
	}
	for {
		select {
		case <-ctx.Done():
			background()
			return ArchivePage{}, ErrUnavailable
		case <-work.Done():
			finish(false)
			return ArchivePage{}, ErrSearchPending
		case <-bus.Done():
			finish(false)
			return ArchivePage{}, ErrUnavailable
		case raw = <-events:
			if !c.currentGeneration(work) || !c.sessionCurrent(work) || bus.Epoch() != epoch {
				finish(false)
				return ArchivePage{}, ErrSessionChanged
			}
			page, e, done, matched = consume(raw)
			if matched && done {
				finish(true)
				return page, e
			}
			if e != nil {
				background()
				return ArchivePage{}, e
			}
		}
	}
}
