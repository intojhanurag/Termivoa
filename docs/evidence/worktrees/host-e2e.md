# Worktrees, host side: end-to-end check (2026-10-01)

Branch `feat/worktrees-host`. An isolated development stack with a temporary
HOME (git `user.name`/`user.email` set there), a temporary data and runtime
directory, and a free loopback port (not 8788). The production broker and
gateway were not touched.

Setup:

```sh
go build -o $T/pterm ./cmd/pterm
HOME=$T/home pterm up --dev --data-dir $T/data --runtime-dir $T/rt \
  --listen 127.0.0.1:<free port> --preview-listen off
pterm project add --dev --runtime-dir $T/rt --name demo $T/home/src/demo
```

`demo` is a clone of a local bare repository with `main` and `feature`, and
`origin/HEAD` points to `origin/main`. A temporary Go driver (not committed)
paired over `/api/v1/pair/redeem` and `pterm pair approve`, then used the HTTP
API and the `/api/v1/ws` live socket as the phone does.

Output (`$T` is `/tmp/wte2e.8ehS`):

```text
paired: 200 true
source session: 200 4de556bd-eb5e-401f-86ba-dca3b1995fa2 worktree: <nil>
branches.listed: {"branches":[{"name":"feature","remote":false},{"name":"main","remote":false},{"name":"origin/feature","remote":true},{"name":"origin/main","remote":true}],"current":"main","defaultBranch":"main","repo":"demo","state":"ready","truncated":false}
worktree create: 200 title: brave-otter
  record.worktree: {"name":"brave-otter","repo":"demo"}
  branch in /tmp/wte2e.8ehS/home/termivoa/worktrees/demo/brave-otter = termivoa/brave-otter | HEAD == origin/feature: true | upstream: ERR fatal: no upstream configured for branch 'termivoa/brave-otter'
replay: 200 same session: true | worktrees: /private/tmp/wte2e.8ehS/home/src/demo                            ac296c0 [main]
/private/tmp/wte2e.8ehS/home/termivoa/worktrees/demo/brave-otter 8b417af [termivoa/brave-otter]
same name: 409 {"error":{"code":"worktree_exists","message":"a worktree or branch with this name already exists"}}
bad base: 400 {"error":{"code":"invalid_request","message":"the base is not a branch of this repository"}}
bad name: 400 {"error":{"code":"invalid_request","message":"invalid session request"}}
listed worktree session cwd: /private/tmp/wte2e.8ehS/home/termivoa/worktrees/demo/brave-otter worktree: {brave-otter demo}
branches in worktree: repo demo current termivoa/brave-otter default main
remove without force: {"state":"dirty"}
remove with force: {"state":"removed"}
folder gone: true | branch kept: true
session true exited <nil>
session false running <nil>
remove in a project session: error {"code":"invalid_request","message":"this session did not start in a Termivoa worktree"}
```

Results: `branches.list` lists local branches first, then remote ones, without
`origin/HEAD`. The default is `main`. The create made
`$HOME/termivoa/worktrees/demo/brave-otter` on `termivoa/brave-otter` at
`origin/feature`, with no upstream. The session's live folder (read from the
OS) is that folder. A replay returned the same session, and only one worktree
exists. An existing name gives 409 `worktree_exists`. A bad base or name gives
400. Inside the worktree, the listing shows the main repository name. Remove
without force gave `dirty`, and with force gave `removed`. The folder is gone,
the branch is kept, and the session exited. In a project session, remove is
refused.
