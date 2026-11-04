package l1admission_test

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/openkcm/platform-mesh-controller/internal/l1admission"
)

const reviewUID = types.UID("3f1c2b8e-6a0d-4d5e-9f7a-1b2c3d4e5f60")

func startTestServer(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = listener.Close() })

	server := l1admission.NewServer(listener.Addr().String())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	return "http://" + listener.Addr().String()
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err, "build request")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "post")
	t.Cleanup(func() { _ = resp.Body.Close() })

	return resp
}

func decode(t *testing.T, resp *http.Response) admissionv1.AdmissionReview {
	t.Helper()

	var review admissionv1.AdmissionReview
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&review), "decode review")
	require.NotNil(t, review.Response, "review without response")

	return review
}

func TestAllowsRootKeyReview(t *testing.T) {
	// given
	url := startTestServer(t) + l1admission.Path
	body, err := json.Marshal(admissionv1.AdmissionReview{
		APIVersion: "admission.k8s.io/v1",
		Kind:       "AdmissionReview",
		Request: &admissionv1.AdmissionRequest{
			UID:       reviewUID,
			Operation: admissionv1.Create,
			Resource: metav1.GroupVersionResource{
				Group:    "operations.openkcm.io",
				Version:  "v1alpha1",
				Resource: "awsrootkeys",
			},
			Object: runtime.RawExtension{Raw: []byte(`{"kind":"AWSRootKey"}`)},
		},
	})
	require.NoError(t, err, "encode review")

	// when
	resp := post(t, url, string(body))

	// then
	require.Equal(t, http.StatusOK, resp.StatusCode)
	review := decode(t, resp)
	require.Equal(t, reviewUID, review.Response.UID)
	require.True(t, review.Response.Allowed)
}

func TestRejectsMalformedReview(t *testing.T) {
	// given
	url := startTestServer(t) + l1admission.Path

	// when
	resp := post(t, url, "{not json")

	// then
	review := decode(t, resp)
	require.False(t, review.Response.Allowed)
	require.Equal(t, int32(http.StatusBadRequest), review.Response.Result.Code)
}

func TestServesOnlyTheWebhookPath(t *testing.T) {
	// given
	url := startTestServer(t) + "/validate"

	// when
	resp := post(t, url, "{}")

	// then
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
