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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"go.zoe.im/x/cli"
)

type peersCmd struct {
	APIAddr string `opts:"short=a,help=swarm API address (host:port)"`
	JSON    bool   `opts:"short=j,help=output as JSON instead of table"`
}

func init() {
	c := &peersCmd{
		APIAddr: "localhost:8080",
	}
	app.Register(cli.New(
		cli.Name("peers"),
		cli.Short("List connected P2P peers"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if len(args) > 0 && args[0] == "connect" {
				if len(args) < 2 {
					fmt.Fprintln(os.Stderr, "Usage: spore peers connect <multiaddr>")
					os.Exit(1)
				}
				if err := connectPeer(c.APIAddr, args[1]); err != nil {
					fmt.Fprintln(os.Stderr, "Error:", err)
					os.Exit(1)
				}
				return
			}
			if err := c.run(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

func (c *peersCmd) run() error {
	url := fmt.Sprintf("http://%s/api/peers", c.APIAddr)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("connecting to swarm API at %s: %w\n\nMake sure a swarm is running with --api-port", c.APIAddr, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		PeerID string   `json:"peer_id"`
		Peers  []string `json:"peers"`
		Count  int      `json:"count"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if c.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("Local peer ID: %s\n", result.PeerID)
	if len(result.Peers) == 0 {
		fmt.Println("No connected peers.")
		return nil
	}

	fmt.Printf("\nConnected peers (%d):\n", result.Count)
	for _, p := range result.Peers {
		fmt.Printf("  %s\n", p)
	}
	return nil
}

func connectPeer(apiAddr, multiaddr string) error {
	url := fmt.Sprintf("http://%s/api/peers/connect?addr=%s", apiAddr, multiaddr)
	resp, err := http.Post(url, "", nil)
	if err != nil {
		return fmt.Errorf("connecting to swarm API at %s: %w", apiAddr, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("connect failed: %s", string(body))
	}

	fmt.Printf("Connected to %s\n", multiaddr)
	return nil
}
