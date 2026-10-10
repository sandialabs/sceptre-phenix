# Command Line

Builder has three `phenix` command groups:

| Command | What it does | Works on |
|---|---|---|
| `phenix builder publish` | Makes a topology from a Builder file | The phenix store, directly (see [From the command line](import-upload-download.md#from-the-command-line)) |
| `phenix builder drafts` | Lists drafts, exports one, checks whether one can be published, and runs its preflight checks | A running phenix server, through its REST API |
| `phenix builder templates` | Lists Node Templates, exports them as a template file, and imports a template file | A running phenix server, through its REST API |

`drafts` and `templates` need no browser, so scripts and CI can use them.
They print tables for people, and JSON or YAML of a fixed shape for
scripts (`-o json`, `-o yaml`), and their exit status says how a check
went.

## Connecting to the server

With `--url`, the commands send their requests to that phenix server, as
the user of the API token that `--token` gives:

```bash
phenix builder drafts list --url https://phenix.example --token "$PHENIX_TOKEN"
```

| Flag | Environment variable | What it is |
|---|---|---|
| `--url` | `PHENIX_URL` | The URL of the phenix server, `http` or `https`, with the path a proxy serves phenix at, if any |
| `--token` | `PHENIX_TOKEN` | An API token. The commands send it in the `X-Phenix-Auth-Token: Bearer <token>` header. It needs `--url` |

A flag wins over its environment variable. A flag given an empty value,
such as `--token "$CI_TOKEN"` when `CI_TOKEN` is unset, counts as not
given, so the environment variable still applies. Prefer `PHENIX_TOKEN`
to `--token`: other users of the host can see the arguments of a running
command. The commands never print the token.

The commands do not follow redirects, so the token goes to no other
server. When the server answers with a redirect, such as a proxy that
sends `http` to `https`, the command exits 2 and names where the redirect
points: give that URL with `--url`.

The token's user decides what the requests may see and do: they have the
user's role, as the web UI has (see [Permissions](administration.md#permissions)).
A user creates a token in the **Users** tab of the web UI, or with
`POST /api/v1/users/{username}/tokens` (see [API](../api.md)). A server with
sign-in off needs no token. The URL must not hold a user name or a
password.

Without `--url` or `PHENIX_URL`, the commands send their requests to the
unix socket of `phenix ui` on the same host: the socket the global
`--unix-socket` flag names, `/tmp/phenix.sock` by default, which
`phenix workflow apply` uses too. There, every request acts as
`global-admin`. A token without a URL, from `--token` or `PHENIX_TOKEN`,
is refused (exit 2, "--token needs --url; without --url the commands use
the local socket as global-admin") rather than left unsent. The socket's
file mode decides who may connect: the user that runs `phenix ui`, and with
`phenix ui --unix-socket-gid` the members of that group. With Docker, run
the command in the container:

```bash
docker exec phenix phenix builder drafts list
```

## Drafts

A draft is named `<owner>/<draft id>`, as `phenix builder drafts list`
shows it.

### Listing drafts

```bash
phenix builder drafts list [--shared] [--owner <user>] [-o table|json|yaml]
```

Lists your own drafts. `--shared` adds the drafts of other users you can
see: those shared with you, and those your role may list. `--owner`
lists only the drafts of that user, from either. Drafts are sorted by
owner, then by ID. A draft this server can no longer read is not listed.

```console
$ phenix builder drafts list --shared
+-------+-----------+--------------+----------------------+------------+--------+
| OWNER |   DRAFT   |     NAME     |       UPDATED        | UPDATED BY | ACCESS |
+-------+-----------+--------------+----------------------+------------+--------+
| alice | riverside | Riverside    | 2026-10-02T10:30:00Z | alice      | owner  |
| bob   | pumps     | Pump station | 2026-10-04T12:00:00Z | carol      | edit   |
+-------+-----------+--------------+----------------------+------------+--------+
```

With `-o json`:

```json
{
  "drafts": [
    {
      "owner": "alice",
      "id": "riverside",
      "name": "Riverside",
      "updatedAt": "2026-10-02T10:30:00Z",
      "updatedBy": "alice",
      "access": "owner"
    }
  ]
}
```

`access` is `owner`, `edit` or `view`. A draft without a name has no
`name`.

### Exporting a draft

```bash
phenix builder drafts export <owner>/<draft> [--format json|yaml] [--output <file>]
phenix builder drafts export <owner>/<draft> --package [--include scenarios,topologies,icons,images]
```

Writes the current document of the draft as Builder JSON (the default) or
Builder YAML, to standard output or to the file `--output` names. The
same document always gives the same bytes, so an export can be committed
and compared. With `--package`, the command writes the Builder package the
server makes of the document instead, holding the parts `--include`
names.

```bash
phenix builder drafts export alice/riverside --output riverside.builder.json
```

### Checking whether a draft can be published

```bash
phenix builder drafts validate <owner>/<draft> [-o table|json|yaml]
```

Checks the current document of the draft as the Topology YAML download
does (see [Downloading](import-upload-download.md)): errors are what would
block publishing it, warnings what publishing would warn about. Nothing is
written. The command exits 1 when the draft has errors.

```console
$ phenix builder drafts validate alice/riverside
Draft alice/riverside (Riverside) cannot be published: 1 error, 0 warnings.
+----------+------+---------+------------------------------------------+
| SEVERITY | CODE | ELEMENT |                 MESSAGE                  |
+----------+------+---------+------------------------------------------+
| error    |      |         | interface eth0 of device web has no VLAN |
+----------+------+---------+------------------------------------------+
```

With `-o json`:

```json
{
  "draft": {
    "owner": "alice",
    "id": "riverside",
    "name": "Riverside"
  },
  "valid": false,
  "errors": [
    {
      "severity": "error",
      "message": "interface eth0 of device web has no VLAN"
    }
  ],
  "warnings": []
}
```

Each issue has a `severity` (`error` or `warning`) and a `message`, and
when the server gives them, a `code`, the `path` of the element in the
document, its `nodeId`, `edgeId` or `networkId`, and the `field` of the
element. A document the server refuses to check at all, such as one with
a hostname of one character or a malformed MAC address, is not valid: the
issues the refusal lists are its errors and warnings, and when it lists no
error, the refusal's message and the reason the server gives with it are
its one error.

### Running preflight checks

```bash
phenix builder drafts preflight <owner>/<draft> [--check capacity,network,disks,apps] [--experiment <name>] [--strict] [-o table|json|yaml]
```

Asks the server whether an experiment of the draft could run: whether the
cluster has the `capacity`, the `network`s, the `disks` (disk images) and
the `apps` it needs. `--check` names the checks to run, all four by
default, and `--experiment` the experiment they are for. Each check
`passed`, `failed`, or is `unavailable` when the server could not run it.
The command exits 1 when a check failed, and with `--strict` also when a
check is unavailable. The JSON is the server's answer:

```json
{
  "checks": [
    {
      "name": "disks",
      "status": "failed",
      "summary": "1 disk image is missing",
      "issues": [
        {
          "code": "disk-missing",
          "severity": "error",
          "message": "disk image win10.qc2 does not exist",
          "nodeId": "n-ws"
        }
      ]
    }
  ],
  "passed": [],
  "failed": [
    "disks"
  ],
  "unavailable": []
}
```

## Node Templates

### Listing templates

```bash
phenix builder templates list [--owner <user>] [-o table|json|yaml]
```

Lists the collections and templates you can use. Each has a `source`:

| Source | What it is |
|---|---|
| `mine` | In your library |
| `built-in` | One of the built-in templates, in your library |
| `shared` | In another user's library, shared with you |
| `server-wide` | In another user's library, published to every user |
| `server` | Read by the server from its template files (see [Template files on the server](administration.md#template-files-on-the-server)) |

`--owner` lists only the items of that user. With `-o json`, `user` is
you, a collection lists the IDs of its `templates`, and a template the
IDs of the `collections` that hold it:

```json
{
  "user": "alice",
  "collections": [
    {
      "id": "0f6c2b5e",
      "name": "Lab",
      "owner": "alice",
      "source": "mine",
      "templates": [
        "8d1e4a77"
      ]
    }
  ],
  "templates": [
    {
      "id": "server",
      "name": "Server",
      "description": "Generic Linux server",
      "owner": "alice",
      "source": "built-in",
      "collections": []
    },
    {
      "id": "8d1e4a77",
      "name": "PLC",
      "owner": "alice",
      "source": "mine",
      "collections": [
        "0f6c2b5e"
      ]
    }
  ]
}
```

### Exporting templates

```bash
phenix builder templates export [--collection <name or ID>] [--owner <user>] [--format yaml|json] [--output <file>]
```

Writes a template file (see [Template files](templates.md)), the format
**Export** saves and the server reads from its template directory: the
templates of the collection `--collection` names, else every template you
can use, of `--owner` only when it is given. The file carries the custom
icons the templates use, from the icon library. Templates whose names
differ only in case are numbered apart, " (2)", " (3)" and so on, with a
warning on standard error. When two collections have the name you give,
give `--owner` or the collection's ID.

```bash
phenix builder templates export --collection Substation --output substation.templates.yaml
```

### Importing a template file

```bash
phenix builder templates import <file> [--name <collection>]
```

Reads a template file, YAML or JSON, as **Import** on the **Node
Templates** tab does, and adds its templates to your library as a new
collection. The collection is named as the file names it, or `--name`;
when one of your collections already has that name, ignoring case, the
command adds " (2)", " (3)" and so on. The custom icons the file carries
are added to the icon library when it lacks them. An icon whose name the
library holds with another image is left out with a warning, and the
templates show the library's icon.

```console
$ phenix builder templates import substation.templates.yaml
Imported 12 templates as collection Substation (2).
```

## Exit status

| Status | Meaning |
|---|---|
| 0 | Success. `validate`: the draft has no errors. `preflight`: no check failed (with `--strict`, none was unavailable either) |
| 1 | The server answered with findings: `validate` found errors, or a preflight check failed (with `--strict`, or was unavailable) |
| 2 | The request could not be made or was refused: no connection, a redirect, a refused token, no permission, a draft or collection that does not exist, an unknown subcommand, or invalid arguments or files |

The report is written before the command exits 1, so a script can read it
and still fail the step.

## In CI

A CI job can check that a draft can be published, and keep the report:

```bash
phenix builder drafts validate alice/riverside --url https://phenix.example --token "$PHENIX_TOKEN" -o json > validate.json
```

The step fails when the draft has errors (1) or when the server could not
be asked (2). To keep the diagram with the code that uses it:

```bash
phenix builder drafts export alice/riverside --url https://phenix.example --format yaml --output riverside.builder.yaml
```
