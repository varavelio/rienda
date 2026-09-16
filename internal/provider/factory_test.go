package provider

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("builds a distinct client per supported protocol", func(t *testing.T) {
		seen := map[reflect.Type]Protocol{}
		for _, protocol := range Protocols() {
			client, err := New(protocol, Config{APIKey: "key", BaseURL: "https://example.com"})

			require.NoError(t, err, "protocol %q", protocol)
			require.NotNil(t, client, "protocol %q", protocol)

			clientType := reflect.TypeOf(client)
			require.NotContains(t, seen, clientType, "protocol %q", protocol)
			seen[clientType] = protocol
		}
	})

	t.Run("rejects an unknown protocol", func(t *testing.T) {
		client, err := New("gemini", Config{})

		require.Error(t, err)
		require.ErrorContains(t, err, `unknown protocol "gemini"`)
		require.Nil(t, client)
	})
}
