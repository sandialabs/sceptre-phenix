<template>
  <div class="content tunneler">
    <section id="downloads" aria-labelledby="downloads-heading">
      <h1 id="downloads-heading">Tunneler Downloads</h1>
      <p v-if="notInstalled">
        Tunneler downloads are not installed on this phēnix server.
      </p>
      <b-table v-else :data="downloads">
        <template #empty>
          <div class="has-text-centered">
            {{
              failed
                ? 'Could not list the tunneler downloads'
                : loaded
                  ? 'No tunneler builds are installed on this server'
                  : 'Loading downloads…'
            }}
          </div>
        </template>
        <b-table-column label="OS" v-slot="props">
          {{ props.row.os }}
        </b-table-column>
        <b-table-column label="Architecture" v-slot="props">
          {{ props.row.arch }}
        </b-table-column>
        <b-table-column label="Download" centered v-slot="props">
          <a
            :href="props.row.link"
            download
            :aria-label="`Download ${props.row.file}`"
            :title="props.row.file">
            <b-icon icon="file-download" size="is-small"></b-icon>
          </a>
        </b-table-column>
      </b-table>
    </section>
    <hr />
    <div id="quick-start">
      <div class="quick-start-head">
        <h2>Quick Start</h2>
        <b-button
          tag="a"
          :href="docsPage('tunneler')"
          target="_blank"
          rel="noopener"
          type="is-light"
          size="is-large"
          icon-left="book"
          class="docs-button"
          >Tunneler Documentation</b-button
        >
      </div>
      <p>
        The phēnix-tunneler is an application phēnix users can run on their
        local machine in order to access services in VMs locally.

        <br />
        <br />

        For example, say a Windows VM in a phēnix experiment is accessible via
        Remote Desktop Protocol (RDP) and a user wants to use a RDP client to
        access the VM instead of noVNC via the browser (e.g., for copy/paste
        support).

        <br />
        <br />

        Once the appropriate phenix-tunneler executable is downloaded via the
        table above, users can do the above by starting the phenix-tunneler
        server and then creating a new port forward for a VM in the phēnix UI.

        <br />
        <br />

        To start the phenix-tunneler server, run
        <code>phenix-tunneler serve full-url-to-phenix</code>, passing it either
        the <code>--username</code> or <code>--auth-token</code> option if
        authentication is enabled in the phēnix UI.

        <br />
        <br />

        The <code>phenix-tunneler serve</code> command can also provide a local
        web interface for listing and managing listeners. It is disabled by
        default; enable it with the <code>--web-listen 127.0.0.1:8080</code>
        flag (the address or port can be changed if needed).

        <br />
        <br />

        Once the phenix-tunneler server is running locally, it will
        automatically get notified of port forwards created in the UI. If the
        same user that logged into the phenix-tunneler server is the same user
        that creates the port forward in the UI, the local port will be
        activated automatically.

        <br />
        <br />

        Once a local port is activated, either automatically or manually, users
        can connect to the local port with the appropriate application and
        traffic will be forwarded through the phēnix UI server to the VM.
      </p>
    </div>
  </div>
</template>

<script>
  import { docsPage } from '@/utils/docs.js';

  // how the builds the server may offer are named on the page
  const BUILDS = {
    'phenix-tunneler-linux-amd64': { os: 'Linux', arch: 'amd64' },
    'phenix-tunneler-darwin-arm64': { os: 'macOS', arch: 'arm64' },
    'phenix-tunneler-darwin-amd64': { os: 'macOS', arch: 'amd64' },
    'phenix-tunneler-windows-amd64.exe': { os: 'Windows', arch: 'amd64' },
  };

  const downloadsPath = `${import.meta.env.BASE_URL}downloads/tunneler`;

  export default {
    setup() {
      return { docsPage };
    },

    async created() {
      // only the builds actually installed, so no link leads to a 404
      try {
        const resp = await fetch(downloadsPath);
        // without downloads the server has no such route, and answers with
        // the app's own page
        if (!resp.headers.get('content-type')?.includes('application/json')) {
          this.notInstalled = resp.ok || resp.status === 404;
          if (!this.notInstalled) this.failed = true;
          return;
        }
        const { files } = await resp.json();
        this.downloads = (files ?? []).map((file) => ({
          file,
          os: BUILDS[file]?.os ?? file,
          arch: BUILDS[file]?.arch ?? '',
          link: `${downloadsPath}/${encodeURIComponent(file)}`,
        }));
      } catch {
        this.failed = true;
      } finally {
        this.loaded = true;
      }
    },

    data() {
      return {
        downloads: [],
        loaded: false,
        failed: false,
        notInstalled: false,
      };
    },
  };
</script>

<style scoped lang="scss">
  p {
    color: whitesmoke !important;
  }

  div.tunneler {
    width: 60%;
    min-width: min(100%, 36rem);
    margin: auto;
  }

  section#downloads {
    max-width: 36rem;
  }

  .quick-start-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem 1.5rem;
    margin-bottom: 1em;
  }

  .quick-start-head h2 {
    margin: 0;
  }

  #quick-start p {
    font-size: calc(1em + 2pt);
  }

  code {
    background-color: black;
  }
</style>
