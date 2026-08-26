package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/medianotification"
)

type fakeMediaNotificationService struct {
	page medianotification.Page
	subscriptions []medianotification.Subscription
	followed medianotification.Subscription
	cursor string
	limit int
	id string
	state string
	err error
}

func (service *fakeMediaNotificationService) ListSubscriptions(context.Context, auth.Principal) ([]medianotification.Subscription, error) { return service.subscriptions, service.err }
func (service *fakeMediaNotificationService) Follow(_ context.Context, _ auth.Principal, _ string, _ medianotification.FollowInput) (medianotification.Subscription, error) { return service.followed, service.err }
func (service *fakeMediaNotificationService) Unfollow(context.Context, auth.Principal, string) error { return service.err }
func (service *fakeMediaNotificationService) List(_ context.Context, _ auth.Principal, cursor string, limit int) (medianotification.Page, error) { service.cursor, service.limit = cursor, limit; return service.page, service.err }
func (service *fakeMediaNotificationService) Acknowledge(_ context.Context, _ auth.Principal, id, state string) error { service.id, service.state = id, state; return service.err }
func (service *fakeMediaNotificationService) RunScheduled(context.Context) error { return service.err }

func TestMediaNotificationInboxForwardsCursorAndLimit(t *testing.T) {
	service := &fakeMediaNotificationService{page: medianotification.Page{Notifications: []medianotification.Notification{{ID: "12", Kind: medianotification.KindMovieRelease, TitleID: "title", Title: "Release", AvailableAt: time.Unix(0, 0).UTC(), CreatedAt: time.Unix(0, 0).UTC()}}, NextCursor: "12"}}
	api := &API{mediaNotifications: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/media-notifications?cursor=42&limit=7", nil)
	api.mediaNotificationInbox(response, request, auth.Principal{})
	if response.Code != http.StatusOK || service.cursor != "42" || service.limit != 7 { t.Fatalf("inbox status=%d cursor=%q limit=%d", response.Code, service.cursor, service.limit) }
	if !strings.Contains(response.Body.String(), `"kind":"movie-release"`) || !strings.Contains(response.Body.String(), `"nextCursor":"12"`) { t.Fatalf("inbox body = %s", response.Body.String()) }
}

func TestMediaNotificationAcknowledgementIsBoundedAndClosed(t *testing.T) {
	service := &fakeMediaNotificationService{}
	api := &API{mediaNotifications: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/media-notifications/9/acknowledgement", strings.NewReader(`{"state":"dismissed"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("notificationId", "9")
	response := httptest.NewRecorder()
	api.acknowledgeMediaNotification(response, request, auth.Principal{})
	if response.Code != http.StatusNoContent || service.id != "9" || service.state != "dismissed" { t.Fatalf("ack status=%d id=%q state=%q", response.Code, service.id, service.state) }

	request = httptest.NewRequest(http.MethodPost, "/api/v1/media-notifications/9/acknowledgement", strings.NewReader(`{"state":"dismissed","extra":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("notificationId", "9")
	response = httptest.NewRecorder()
	api.acknowledgeMediaNotification(response, request, auth.Principal{})
	if response.Code != http.StatusBadRequest { t.Fatalf("unknown acknowledgement field status = %d", response.Code) }
}

func TestMediaNotificationErrorsDoNotLeakForeignProfileRows(t *testing.T) {
	service := &fakeMediaNotificationService{err: medianotification.ErrNotFound}
	api := &API{mediaNotifications: service, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/media-notifications/77/acknowledgement", strings.NewReader(`{"state":"read"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("notificationId", "77")
	response := httptest.NewRecorder()
	api.acknowledgeMediaNotification(response, request, auth.Principal{})
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"media_notification_not_found"`) { t.Fatalf("foreign notification response = %d %s", response.Code, response.Body.String()) }
}
