package file

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "UploadDeltaRequest", uploadDeltaRequest{})
	apidocstest.AssertMatches(t, "Delta", deltaResponse{})
	apidocstest.AssertMatches(t, "RenameFileRequest", renameFileRequest{})
	apidocstest.AssertMatches(t, "HistoryEntry", historyEntryResponse{})
	apidocstest.AssertMatches(t, "SyncEvent", syncEventResponse{})
	apidocstest.AssertMatches(t, "FileInfo", fileInfoResponse{})
	apidocstest.AssertMatches(t, "BulkFileInfoRequest", bulkGetFileInfoRequest{})
	apidocstest.AssertMatches(t, "BatchRequest", batchRequest{})
	apidocstest.AssertMatches(t, "BatchFrameHeader", batchFrameHeader{})
	apidocstest.AssertMatches(t, "UpdateCursorRequest", updateCursorRequest{})
}
