/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package l1admission answers the admission reviews that kcp sends to the L1 access webhook
// of the OpenKCM APIExport.
package l1admission

import (
	"context"
	"errors"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Path is the path of the webhook URL in the ValidatingWebhookConfiguration.
const Path = "/validate-l1-access"

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// NewServer returns the HTTP server of the webhook. TLS is terminated in front of it.
func NewServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(Path, &admission.Webhook{Handler: admission.HandlerFunc(allow)})
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: readHeaderTimeout}
}

// Start serves the webhook until ctx is done.
func Start(ctx context.Context, addr string) error {
	srv := NewServer(addr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func allow(context.Context, admission.Request) admission.Response {
	// TODO: there is no L1 Key Manager check yet, so every review is allowed.
	return admission.Allowed("")
}
