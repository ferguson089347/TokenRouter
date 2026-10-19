package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// calendarPublicSource supplies empty settings so the test exercises the injected calendar.
type calendarPublicSource struct{}

// ticketPublicSource supplies ticket availability for API and HTML comparison.
type ticketPublicSource map[string]string

// TestPublicCalendarMatchesAPIAndInjection supplies calendars with distinct zone names and display labels.
// It verifies that API and HTML injection return the configured label and the
// current UTC offset derived from the same injected calendar.
func TestPublicCalendarMatchesAPIAndInjection(t *testing.T) {
	for _, item := range []struct {
		location, name string // IANA calendar location and displayed timezone label
	}{
		{"Asia/Shanghai", "Asia/Shanghai"},
		{"America/New_York", "America/New_York"},
		{"America/New_York", "Local"},
	} {
		t.Run(item.name, func(t *testing.T) {
			location, err := time.LoadLocation(item.location)
			require.NoError(t, err)
			calendar := timezone.NewCalendar(location)
			service := site.NewPublicService(calendarPublicSource{}, calendar, item.name)
			handler := NewPublicHandler(service, "calendar-version")
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/settings/public", nil)
			handler.GetPublicSettings(ctx)
			require.Equal(t, 200, response.Code)
			var envelope struct {
				Data map[string]any `json:"data"` // public settings response
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			injection, err := service.GetPublicSettingsForInjection(context.Background())
			require.NoError(t, err)
			encoded, err := json.Marshal(injection)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(encoded, &fields))
			for _, values := range []map[string]any{envelope.Data, fields} {
				require.Equal(t, item.name, values["server_timezone"])
				require.Equal(t, calendar.UTCOffset(time.Now()), values["server_utc_offset"])
			}
		})
	}
}

// LoadSitePublicInputs supplies empty display, auth, and usage inputs for calendar tests.
// Each call allocates a fresh values map, keeping fixture data independent of
// the response service's language and navigation transformations.
func (calendarPublicSource) LoadSitePublicInputs(context.Context) (site.PublicInputs, error) {
	return site.PublicInputs{Values: map[string]string{}}, nil
}

// PublicVersion returns the fixed source build label used by injection fixtures.
func (calendarPublicSource) PublicVersion() string { return "calendar-contract" }

// LoadSitePublicInputs returns the selected raw ticket setting for public resolution.
func (s ticketPublicSource) LoadSitePublicInputs(context.Context) (site.PublicInputs, error) {
	return site.PublicInputs{Values: s}, nil
}

// PublicVersion identifies the support availability fixture in public responses.
func (ticketPublicSource) PublicVersion() string { return "ticket-test" }

// TestTicketAvailabilityMatchesAPIAndInjection checks enabled defaults and an explicit
// administrator disable. It verifies anonymous settings and injected HTML agree
// while neither response exposes private ticket records.
func TestTicketAvailabilityMatchesAPIAndInjection(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		service := site.NewPublicService(ticketPublicSource{"ticket_enabled": value}, timezone.NewCalendar(time.UTC), "UTC")
		handler := NewPublicHandler(service, "ticket-test")
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/settings/public", nil)
		handler.GetPublicSettings(ctx)
		require.Equal(t, 200, response.Code)
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		injected, err := service.GetPublicSettingsForInjection(context.Background())
		require.NoError(t, err)
		encoded, err := json.Marshal(injected)
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(encoded, &fields))
		for _, public := range []map[string]any{envelope.Data, fields} {
			require.Equal(t, value != "false", public["ticket_enabled"])
			require.NotContains(t, public, "tickets")
		}
	}
}
