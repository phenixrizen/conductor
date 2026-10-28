# Tasks to do

The backlog moved to GitHub on 2026-10-13. Every bug known and every
feature set aside is now an
[issue](https://github.com/rocksolidlabs/conductor/issues), and the work is
on the project board **Conductor**
(https://github.com/users/phenixrizen/projects/3). `AGENTS.md` ("Working an
issue") defines the current task workflow. Undisclosed security findings
belong in this repository's GitHub Draft Security Advisories and remain
private until the owner explicitly authorizes disclosure. This historical
mapping is not a second backlog.

This file keeps only where each of its entries went, for the rounds in
`features.md` and the plans that name "the todo".

| Kind | Entry | Issue |
|---|---|---|
| Bug | A stopped run can be resumed twice | #126 |
| Bug | A run's record offers Resume and Open, and both answer 404 | #127 |
| Bug | A slow shutdown loses run records | #128 |
| Bug | An agent's question is cleared by the end of its turn | #129 |
| Bug | A viewer holding the terminal's size can lose it when it falls back to the relay | #130 |
| Bug | The SIGNAL size limit holds one way only | #131 |
| Bug | A malformed save part gets an error that names no save | #132 |
| Bug | A second controller's late click answers the next question | #133 |
| Bug | A hosted session's end drops what its link viewers had queued | #134 (fixed by #124, closed) |
| Bug | A new turn's bell within half a second of the last turn's is lost | #135 |
| Bug | Tests that fail under load, older than round 15's last changes | #136 |
| Bug | Small things seen in the rc.7 app (2026-10-11, 1440×900) | #137 |
| Bug | The Explorer's filter takes no path, and stays on after a folder is clicked | #138 |
| Feature | The sidebar, watched in use | #139 |
| Feature | The app forwards the TLS listener into WSL and maps the router from Windows | #140 |
| Feature | Share tokens in the OS keychain | #141 |
| Feature | Signing and notarisation of the desktop builds | #142 |
| Feature | A second factor for a control link | #143 |
| Feature | A QR code to share a session to a phone | #144 |
| Feature | Egress off the switchyard | #145 |
| Feature | The workbench on a CDN | #146 |
| Feature | The join route answering a browser origin named in Settings | #147 |
| Feature | A forgotten run revoking its links at the switchyard at once | #148 |
| Feature | Switchyard rosters and invite lifetimes beyond the share store's | #149 |
| Feature | TURN credential minting | #150 |
| Feature | Progress reported by the agents: how far a run's goal is, and each member's part of it | #151 |
| Feature | Hosted crews | #152 |
| Feature | Resuming a run from its record after a server restart | #153 |
| Feature | An outcome, not only "done", for unattended runs | #154 |
| Feature | A readiness wait for a resumed member | #155 |
| Feature | `conductor up --resume <run>` | #156 |
| Feature | The agent's session id through the plugin agents | #157 |
| Feature | aider's chat-history file as its session handle | #158 |
| Feature | A resume record that outlives `exitedRetention` | #159 |
| Feature | A Capacitor app | #160 |
| Feature | Yolo and Resume for hosted sessions | #161 |
| Feature | A multi-session host protocol | #162 |
| Feature | A Windows build of `conductor host` | #163 |
