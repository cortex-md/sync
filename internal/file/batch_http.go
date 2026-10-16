package file

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/semaphore"
)

const (
	BatchContentType   = "application/vnd.cortex.batch"
	batchFetchWorkers  = 8
	batchBytesInFlight = 16 << 20
)

type batchRequest struct {
	Items []struct {
		Path    string `json:"path"`
		Version int    `json:"version"`
	} `json:"items"`
}

type batchFrameHeader struct {
	Path            string `json:"path"`
	Version         int    `json:"version"`
	SnapshotVersion int    `json:"snapshot_version,omitempty"`
	Status          string `json:"status"`
	SnapshotID      string `json:"snapshot_id,omitempty"`
	Checksum        string `json:"checksum,omitempty"`
	SizeBytes       int64  `json:"size_bytes"`
	CreatedBy       string `json:"created_by,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
}

type fetchedBlob struct {
	data []byte
	err  error
}

func (h *Handler) BatchDownload(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var req batchRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	items := make([]BatchItem, 0, len(req.Items))
	for _, item := range req.Items {
		if item.Path == "" || item.Version < 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid batch item")
			return
		}
		items = append(items, BatchItem{Path: item.Path, Version: item.Version})
	}

	plan, err := h.files.PlanBatchDownload(r.Context(), claims.UserID, vaultID, items)
	if err != nil {
		handleFileError(w, err)
		return
	}

	results := make([]chan fetchedBlob, len(plan.Entries))
	weights := make([]int64, len(plan.Entries))
	for i := range results {
		results[i] = make(chan fetchedBlob, 1)
	}
	workers := make(chan struct{}, batchFetchWorkers)
	inFlight := semaphore.NewWeighted(batchBytesInFlight)
	ctx := r.Context()
	go func() {
		for i, entry := range plan.Entries {
			if entry.Snapshot == nil {
				results[i] <- fetchedBlob{}
				continue
			}
			weight := min(max(entry.Snapshot.SizeBytes, 1), batchBytesInFlight)
			if err := inFlight.Acquire(ctx, weight); err != nil {
				results[i] <- fetchedBlob{err: err}
				continue
			}
			weights[i] = weight
			select {
			case workers <- struct{}{}:
			case <-ctx.Done():
				results[i] <- fetchedBlob{err: ctx.Err()}
				continue
			}
			go func(entry BatchEntry, out chan<- fetchedBlob) {
				defer func() { <-workers }()
				out <- fetchBlob(ctx, plan, entry)
			}(entry, results[i])
		}
	}()

	w.Header().Set("Content-Type", BatchContentType)
	w.Header().Set("X-Batch-Count", strconv.Itoa(len(plan.Entries)))
	w.WriteHeader(http.StatusOK)

	out := bufio.NewWriterSize(w, 64<<10)
	for i, entry := range plan.Entries {
		fetched := <-results[i]
		header := frameHeader(entry, fetched.err)
		body := fetched.data
		if header.Status != "ok" {
			body = nil
		}
		err := writeFrame(out, header, body)
		if weights[i] > 0 {
			inFlight.Release(weights[i])
		}
		if err != nil {
			log.Debug().Err(err).Msg("batch download aborted by client")
			return
		}
	}
	_ = out.Flush()
}

func fetchBlob(ctx context.Context, plan *BatchPlan, entry BatchEntry) fetchedBlob {
	reader, err := plan.Open(ctx, entry)
	if err != nil {
		return fetchedBlob{err: err}
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	return fetchedBlob{data: data, err: err}
}

func frameHeader(entry BatchEntry, fetchErr error) batchFrameHeader {
	header := batchFrameHeader{Path: entry.Path, Version: entry.Version, Status: "ok"}
	err := entry.Err
	if err == nil {
		err = fetchErr
	}
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrNotFound):
		header.Status = "not_found"
	case errors.Is(err, domain.ErrInsufficientRole):
		header.Status = "forbidden"
	default:
		header.Status = "error"
	}
	if s := entry.Snapshot; s != nil && header.Status == "ok" {
		header.SnapshotVersion = s.Version
		header.SnapshotID = s.ID.String()
		header.Checksum = s.Checksum
		header.SizeBytes = s.SizeBytes
		header.CreatedBy = s.CreatedBy.String()
		header.CreatedAt = s.CreatedAt.UTC().Format(time.RFC3339)
	}
	return header
}

func writeFrame(w io.Writer, header batchFrameHeader, body []byte) error {
	encoded, err := json.Marshal(header)
	if err != nil {
		return err
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(encoded)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	if _, err := w.Write(encoded); err != nil {
		return err
	}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(body)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

type updateCursorRequest struct {
	LastEventID int64 `json:"last_event_id"`
}

func (h *Handler) UpdateCursor(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}
	var req updateCursorRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if err := h.files.UpdateCursor(r.Context(), UpdateCursorInput{
		UserID:      claims.UserID,
		DeviceID:    claims.DeviceID,
		VaultID:     vaultID,
		LastEventID: req.LastEventID,
	}); err != nil {
		handleFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
