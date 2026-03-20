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

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/nctiggy/nutanix-vma/pkg/mock"
)

func main() {
	port := flag.Int("port", 9440, "port to listen on")
	flag.Parse()

	srv := mock.NewServer(
		mock.WithFixtures(),
		mock.WithCBTConfig(mock.DefaultCBTConfig()),
	)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Mock Nutanix API server listening on %s", addr)
	log.Printf("Fixtures: 3 VMs, 2 subnets, 1 storage container, 1 cluster")

	// Use the handler from the mock server for standalone serving.
	// Note: CBT discovery returns the httptest URL; for standalone use,
	// clients should use the --port address for both PC and PE operations.
	log.Fatal(http.ListenAndServe(addr, srv.Handler()))
}
