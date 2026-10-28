package monitor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChannelMonitorTaxonomyAdaptsProviderTerminology exercises current provider
// wording and historical account wording with the same category. It also
// protects policy precedence and transport classification before generic errors.
func TestChannelMonitorTaxonomyAdaptsProviderTerminology(t *testing.T) {
	for _, test := range []struct {
		input    ChannelMonitorV2ErrorInput // stored status and message evidence
		category string                     // expected versioned taxonomy label
	}{
		{ChannelMonitorV2ErrorInput{Message: "no available providers"}, "account_pool_unavailable"},
		{ChannelMonitorV2ErrorInput{Message: "no available accounts"}, "account_pool_unavailable"},
		{ChannelMonitorV2ErrorInput{Message: "not supported by any configured provider"}, "model_unsupported"},
		{ChannelMonitorV2ErrorInput{StatusCode: 401, Message: "content policy"}, "content_policy"},
		{ChannelMonitorV2ErrorInput{StatusCode: 500, Message: "unexpected eof", ErrorOwner: "provider"}, "transport_or_stream"},
	} {
		require.Equal(t, test.category, ClassifyChannelMonitorV2Error(test.input))
	}
}
