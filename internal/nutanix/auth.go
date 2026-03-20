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

package nutanix

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
)

// setBasicAuth sets Basic Authentication on the request.
func setBasicAuth(req *http.Request, username, password string) {
	req.SetBasicAuth(username, password)
}

// buildTransport creates an http.Transport with optional TLS configuration.
func buildTransport(insecureSkipVerify bool, caCert []byte, disableKeepAlives bool) (*http.Transport, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if insecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true //nolint:gosec // user-controlled via NutanixProvider spec
	}

	if len(caCert) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = pool
	}

	return &http.Transport{
		TLSClientConfig:   tlsConfig,
		DisableKeepAlives: disableKeepAlives,
	}, nil
}
