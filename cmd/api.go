/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmd

import (
	"fmt"

	"go.zoe.im/spore/internal/api"
	"go.zoe.im/spore/internal/swarm"
)

// startAPIServer launches the HTTP API server for the swarm.
func startAPIServer(sw *swarm.Swarm, port int) {
	srv := api.NewServer(sw, port)
	fmt.Printf("🌐 API server listening on :%d\n", port)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Printf("❌ API server error: %v\n", err)
	}
}
