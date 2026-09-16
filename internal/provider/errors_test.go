package provider

import (
	"net/http"
	"testing"

	"github.com/varavelio/rienda/internal/llm"

	"github.com/stretchr/testify/require"
)

func TestErrorKind(t *testing.T) {
	t.Run("classifies failures from wire data and status", func(t *testing.T) {
		cases := []struct {
			name    string
			status  int
			errType string
			code    string
			message string
			want    llm.ErrorKind
		}{
			{
				name: "code wins over type and status", status: http.StatusBadRequest,
				errType: "invalid_request_error", code: "context_length_exceeded",
				message: "too long", want: llm.ErrorKindContextLength,
			},
			{
				name: "quota code wins over rate limit type", status: http.StatusTooManyRequests,
				errType: "rate_limit_error", code: "insufficient_quota",
				message: "no quota", want: llm.ErrorKindQuota,
			},
			{
				name: "message hint beats a generic request type", status: http.StatusBadRequest,
				errType: "invalid_request_error", message: "prompt is too long: 300000 tokens",
				want: llm.ErrorKindContextLength,
			},
			{
				name: "message hint is case insensitive", status: http.StatusBadRequest,
				message: "Maximum Context reached", want: llm.ErrorKindContextLength,
			},
			{
				name:    "invalid request type",
				status:  http.StatusBadRequest,
				errType: "invalid_request_error",
				message: "bad field",
				want:    llm.ErrorKindInvalidRequest,
			},
			{
				name:    "authentication type",
				status:  http.StatusUnauthorized,
				errType: "authentication_error",
				message: "bad key",
				want:    llm.ErrorKindAuthentication,
			},
			{
				name:    "permission type",
				status:  http.StatusForbidden,
				errType: "permission_error",
				message: "no access",
				want:    llm.ErrorKindPermission,
			},
			{
				name:    "not found type",
				status:  http.StatusNotFound,
				errType: "not_found_error",
				message: "no model",
				want:    llm.ErrorKindNotFound,
			},
			{
				name:    "rate limit type",
				status:  http.StatusTooManyRequests,
				errType: "rate_limit_error",
				message: "slow down",
				want:    llm.ErrorKindRateLimit,
			},
			{
				name:    "overloaded type",
				status:  statusOverloaded,
				errType: "overloaded_error",
				message: "busy",
				want:    llm.ErrorKindOverloaded,
			},
			{
				name:    "arguments too large",
				status:  http.StatusBadRequest,
				errType: "request_too_large",
				message: "huge",
				want:    llm.ErrorKindInvalidRequest,
			},
			{
				name:    "server type",
				status:  http.StatusInternalServerError,
				errType: "api_error",
				message: "boom",
				want:    llm.ErrorKindServer,
			},
			{
				name:    "unauthorized status",
				status:  http.StatusUnauthorized,
				message: "no json",
				want:    llm.ErrorKindAuthentication,
			},
			{
				name:    "forbidden status",
				status:  http.StatusForbidden,
				message: "no json",
				want:    llm.ErrorKindPermission,
			},
			{
				name:    "not found status",
				status:  http.StatusNotFound,
				message: "no json",
				want:    llm.ErrorKindNotFound,
			},
			{
				name:    "rate limit status",
				status:  http.StatusTooManyRequests,
				message: "no json",
				want:    llm.ErrorKindRateLimit,
			},
			{
				name:    "overloaded status",
				status:  statusOverloaded,
				message: "no json",
				want:    llm.ErrorKindOverloaded,
			},
			{
				name:    "client error status",
				status:  http.StatusUnprocessableEntity,
				message: "no json",
				want:    llm.ErrorKindInvalidRequest,
			},
			{
				name:    "server error status",
				status:  http.StatusBadGateway,
				message: "no json",
				want:    llm.ErrorKindServer,
			},
			{
				name:    "unclassified failure",
				status:  0,
				message: "network down",
				want:    llm.ErrorKindUnknown,
			},
			{
				name:    "successful status",
				status:  http.StatusOK,
				message: "weird",
				want:    llm.ErrorKindUnknown,
			},
		}

		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				kind := errorKind(
					testCase.status,
					testCase.errType,
					testCase.code,
					testCase.message,
				)

				require.Equal(t, testCase.want, kind)
			})
		}
	})
}

func TestAPIError(t *testing.T) {
	t.Run("maps a nested error object", func(t *testing.T) {
		err := apiError(
			openAIChatCompletionsProviderName,
			http.StatusTooManyRequests,
			[]byte(
				`{"error":{"message":"slow down","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
			),
		)

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, openAIChatCompletionsProviderName, providerErr.Provider)
		require.Equal(t, http.StatusTooManyRequests, providerErr.StatusCode)
		require.Equal(t, llm.ErrorKindRateLimit, providerErr.Kind)
		require.Equal(t, "rate_limit_error", providerErr.Type)
		require.Equal(t, "slow down", providerErr.Message)
	})

	t.Run("falls back to the error code when the type is absent", func(t *testing.T) {
		err := apiError(
			openAIResponsesProviderName,
			http.StatusBadRequest,
			[]byte(`{"error":{"message":"too long","code":"context_length_exceeded"}}`),
		)

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, "context_length_exceeded", providerErr.Type)
		require.Equal(t, llm.ErrorKindContextLength, providerErr.Kind)
	})

	t.Run("keeps a plain text body", func(t *testing.T) {
		err := apiError(anthropicProviderName, http.StatusBadGateway, []byte("  bad gateway  "))

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, "bad gateway", providerErr.Message)
		require.Equal(t, llm.ErrorKindServer, providerErr.Kind)
		require.Empty(t, providerErr.Type)
	})

	t.Run("falls back to the status text on an empty body", func(t *testing.T) {
		err := apiError(anthropicProviderName, http.StatusServiceUnavailable, nil)

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, http.StatusText(http.StatusServiceUnavailable), providerErr.Message)
		require.Equal(t, llm.ErrorKindServer, providerErr.Kind)
	})

	t.Run("ignores malformed error payloads", func(t *testing.T) {
		err := apiError(anthropicProviderName, http.StatusBadRequest, []byte(`{"error":{`))

		var providerErr *llm.Error
		require.ErrorAs(t, err, &providerErr)
		require.Equal(t, `{"error":{`, providerErr.Message)
	})
}
