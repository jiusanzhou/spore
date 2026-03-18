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
	"text/tabwriter"
	"time"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/x/cli"
)

type psCmd struct {
	APIAddr string `opts:"short=a,help=swarm API address (e.g. localhost:8080)"`
	JSON    bool   `opts:"short=j,help=output as JSON"`
}

func init() {
	c := &psCmd{
		APIAddr: "localhost:8080",
	}
	app.Register(cli.New(
		cli.Name("ps"),
		cli.Short("List running agents in a swarm"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := c.run(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

func (c *psCmd) run() error {
	url := fmt.Sprintf("http://%s/api/agents", c.APIAddr)
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
		Agents []agent.Info `json:"agents"`
		Count  int          `json:"count"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if c.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result.Agents)
	}

	if len(result.Agents) == 0 {
		fmt.Println("No agents running.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tROLE\tSTATUS\tMODEL\tTASKS\tUPTIME")
	for _, info := range result.Agents {
		uptime := ""
		if !info.StartedAt.IsZero() {
			uptime = time.Since(info.StartedAt).Truncate(time.Second).String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			info.Name, info.Role, info.Status, info.Model, info.TaskCount, uptime)
	}
	w.Flush()
	fmt.Printf("\n%d agent(s) total\n", result.Count)
	return nil
}
