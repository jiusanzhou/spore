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

package engine

import "strings"

// parsedAction holds the parsed result from LLM output.
type parsedAction struct {
	Raw       string
	ToolName  string
	ToolInput string
	Result    string // non-empty if COMPLETE
}

// parseAction extracts tool calls or completion from LLM text.
func parseAction(text string) (parsedAction, bool) {
	pa := parsedAction{Raw: text}

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "COMPLETE:") {
			pa.Result = strings.TrimSpace(strings.TrimPrefix(line, "COMPLETE:"))
			return pa, true
		}

		if strings.HasPrefix(line, "ACTION:") {
			action := strings.TrimSpace(strings.TrimPrefix(line, "ACTION:"))
			parts := strings.SplitN(action, " ", 2)
			pa.ToolName = parts[0]
			if len(parts) > 1 {
				pa.ToolInput = parts[1]
			}
		}
	}

	return pa, false
}
