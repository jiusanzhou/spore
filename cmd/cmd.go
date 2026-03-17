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
	"go.zoe.im/x/cli"
)

const banner = `
     ___ _ __   ___  _ __ ___ 
    / __| '_ \ / _ \| '__/ _ \
    \__ \ |_) | (_) | | |  __/
    |___/ .__/ \___/|_|  \___|
        |_|
`

type sporeApp struct{}

func (s *sporeApp) ShortDescription() string {
	return "Decentralized AI agent swarm protocol & runtime"
}

var (
	s   = &sporeApp{}
	app = cli.FromStruct(s)

	// Run is the entry point.
	Run = app.Run
)

func init() {
	app.Option(cli.Version("0.1.0-dev"))
}
