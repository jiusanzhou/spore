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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"go.zoe.im/x/cli"
)

type taskCmd struct {
	APIAddr string `opts:"short=a,help=swarm API address (host:port)"`
}

func init() {
	c := &taskCmd{
		APIAddr: "localhost:8080",
	}
	app.Register(cli.New(
		cli.Name("task"),
		cli.Short("Send a task to a running agent"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := c.run(args); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

func (c *taskCmd) run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: spore task <agent-name> <description...>")
	}

	agentName := args[0]
	description := strings.Join(args[1:], " ")

	body, err := json.Marshal(map[string]string{
		"description": description,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s/api/agents/%s/tasks", c.APIAddr, agentName)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("connecting to swarm API at %s: %w\n\nMake sure a swarm is running with --api-port", c.APIAddr, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if resp.StatusCode == http.StatusAccepted {
		fmt.Printf("📋 Task %s queued for %s\n", result["task_id"], agentName)
		return nil
	}

	if errMsg, ok := result["error"]; ok {
		return fmt.Errorf("%v", errMsg)
	}
	return fmt.Errorf("unexpected response %d: %s", resp.StatusCode, string(respBody))
}
