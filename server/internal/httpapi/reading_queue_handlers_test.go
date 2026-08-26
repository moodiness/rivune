package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/readingqueue"
)

type fakeReadingQueueService struct {
	profileID string
	itemID    string
	add       readingqueue.AddInput
	reorder   readingqueue.ReorderInput
	mutation  readingqueue.MutationInput
	queue     readingqueue.Queue
	result    readingqueue.Mutation
	err       error
}

func (f *fakeReadingQueueService) Queue(_ context.Context, _ auth.Principal, profileID string) (readingqueue.Queue, error) {
	f.profileID = profileID
	return f.queue, f.err
}
func (f *fakeReadingQueueService) Add(_ context.Context, _ auth.Principal, profileID string, input readingqueue.AddInput) (readingqueue.Mutation, error) {
	f.profileID, f.add = profileID, input
	return f.result, f.err
}
func (f *fakeReadingQueueService) Update(_ context.Context, _ auth.Principal, profileID, itemID string, _ readingqueue.UpdateInput) (readingqueue.Mutation, error) {
	f.profileID, f.itemID = profileID, itemID
	return f.result, f.err
}
func (f *fakeReadingQueueService) Reorder(_ context.Context, _ auth.Principal, profileID string, input readingqueue.ReorderInput) (readingqueue.Mutation, error) {
	f.profileID, f.reorder = profileID, input
	return f.result, f.err
}
func (f *fakeReadingQueueService) Remove(_ context.Context, _ auth.Principal, profileID, itemID string, input readingqueue.MutationInput) (readingqueue.Mutation, error) {
	f.profileID, f.itemID, f.mutation = profileID, itemID, input
	return f.result, f.err
}
func (f *fakeReadingQueueService) Consume(_ context.Context, _ auth.Principal, profileID, itemID string, input readingqueue.MutationInput) (readingqueue.Mutation, error) {
	f.profileID, f.itemID, f.mutation = profileID, itemID, input
	return f.result, f.err
}

func TestReadingQueueHandlersForwardProfilePayloadAndStableOrder(t *testing.T) {
	service := &fakeReadingQueueService{queue: readingqueue.Queue{Revision: 7, Items: []readingqueue.Item{{ID: "first", Position: 0}, {ID: "second", Position: 1}}}, result: readingqueue.Mutation{Revision: 8, AffectedItemID: "item"}}
	api := &API{readingQueue: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/profile/queue", nil)
	listRequest.SetPathValue("profileId", "profile")
	listResponse := httptest.NewRecorder()
	api.readingQueueItems(listResponse, listRequest, auth.Principal{UserID: "user"})
	if listResponse.Code != http.StatusOK || service.profileID != "profile" || listResponse.Body.String() != `{"revision":7,"items":[{"id":"first","mediaType":"","resourceId":"","title":"","position":0,"createdAt":"0001-01-01T00:00:00Z","updatedAt":"0001-01-01T00:00:00Z"},{"id":"second","mediaType":"","resourceId":"","title":"","position":1,"createdAt":"0001-01-01T00:00:00Z","updatedAt":"0001-01-01T00:00:00Z"}]}`+"\n" {
		t.Fatalf("list response status=%d profile=%q body=%s", listResponse.Code, service.profileID, listResponse.Body.String())
	}

	body := `{"operationId":"11111111-1111-4111-8111-111111111111","expectedRevision":7,"mediaType":"movie","resourceId":"resource","title":"Title"}`
	addRequest := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile/queue/items", strings.NewReader(body))
	addRequest.Header.Set("Content-Type", "application/json")
	addRequest.SetPathValue("profileId", "profile")
	addResponse := httptest.NewRecorder()
	api.addReadingQueueItem(addResponse, addRequest, auth.Principal{})
	if addResponse.Code != http.StatusOK || service.add.ExpectedRevision != 7 || service.add.ResourceID != "resource" {
		t.Fatalf("add response=%d input=%+v body=%s", addResponse.Code, service.add, addResponse.Body.String())
	}
}

func TestReadingQueueHandlersBoundBodiesAndMapConflicts(t *testing.T) {
	api := &API{readingQueue: &fakeReadingQueueService{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	oversized := bytes.Repeat([]byte("x"), readingQueueRequestMaximumBytes+1)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile/queue/items", bytes.NewReader(oversized))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.addReadingQueueItem(response, request, auth.Principal{})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("oversized response = %d %s", response.Code, response.Body.String())
	}

	for _, test := range []struct {
		err  error
		code string
	}{
		{readingqueue.ErrConflict, "reading_queue_conflict"},
		{readingqueue.ErrOperationConflict, "reading_queue_operation_conflict"},
		{readingqueue.ErrCapacity, "reading_queue_capacity"},
	} {
		mapped := httptest.NewRecorder()
		writeReadingQueueError(api, mapped, test.err, "test")
		if mapped.Code != http.StatusConflict || !strings.Contains(mapped.Body.String(), `"code":"`+test.code+`"`) {
			t.Fatalf("mapped %v = %d %s", test.err, mapped.Code, mapped.Body.String())
		}
	}
}
