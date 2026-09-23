<template>
  <div class="content">
    <div id="quick-start">
      <h1>Quick Start</h1>
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
        table below, users can do the above by starting the phenix-tunneler
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
    <hr />
    <b-table :data="data">
      <b-table-column field="name" label="OS" v-slot="props">
        {{ props.row.name }}
      </b-table-column>
      <b-table-column field="arch" label="Architecture" v-slot="props">
        {{ props.row.arch }}
      </b-table-column>
      <b-table-column field="link" label="Download" centered v-slot="props">
        <a :href="props.row.link" @click.prevent="download(props.row)">
          <b-icon icon="file-download" size="is-small"></b-icon>
        </a>
      </b-table-column>
    </b-table>
  </div>
</template>

<script>
  import FileSaver from 'file-saver';

  import axiosInstance from '@/utils/axios.js';
  import { useErrorNotification } from '@/utils/errorNotif';

  export default {
    data() {
      return {
        data: [
          {
            name: 'Linux',
            arch: 'amd64',
            link: this.$router.resolve({ name: 'linux-tunneler' }).href,
          },
          {
            name: 'MacOS',
            arch: 'arm64',
            link: this.$router.resolve({ name: 'macos-arm-tunneler' }).href,
          },
          {
            name: 'MacOS',
            arch: 'amd64',
            link: this.$router.resolve({ name: 'macos-intel-tunneler' }).href,
          },
          {
            name: 'Windows',
            arch: 'amd64',
            link: this.$router.resolve({ name: 'windows-tunneler' }).href,
          },
        ],
      };
    },

    methods: {
      download(tunneler) {
        // Downloads require the auth header, so fetch the file instead of
        // following the link. The link already includes the base path.
        axiosInstance
          .get(tunneler.link, { baseURL: '', responseType: 'blob' })
          .then((response) => {
            FileSaver.saveAs(response.data, tunneler.link.split('/').pop());
          })
          .catch(async (err) => {
            // error bodies arrive as blobs because of the response type
            if (err.response?.data instanceof Blob) {
              err.response.data = await err.response.data.text();
            }

            useErrorNotification(err);
          });
      },
    },
  };
</script>

<style scoped lang="scss">
  p {
    color: whitesmoke !important;
  }

  div#quick-start {
    width: 60%;
    margin: auto;
  }

  code {
    background-color: black;
  }
</style>
