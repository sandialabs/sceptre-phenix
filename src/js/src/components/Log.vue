<template>
  <div class="content">
    <template v-if="disabled">
      <section class="hero is-light is-bold is-large">
        <div class="hero-body">
          <div class="container" style="text-align: center">
            <h1 class="title">
              Nothing to see here... logs have been disabled server-side.
            </h1>
          </div>
        </div>
      </section>
    </template>
    <template v-else-if="logs.length == 0">
      <section class="hero is-light is-bold is-large">
        <div class="hero-body">
          <div class="container" style="text-align: center">
            <h1 class="title">
              There are no logs!
            </h1>
          </div>
        </div>
      </section>
    </template>
    <template v-else>
      <hr>
      <b-field position="is-right">
        <b-tooltip label="change duration of log reporting" type="is-light">
          <b-select :value="this.duration" @input="( value ) => assignDuration( value )">
            <option value="1m">1 min</option>
            <option value="5m">5 min</option>
            <option value="10m">10 min</option>
            <option value="15m">15 min</option>
          </b-select>
        </b-tooltip>
        &nbsp; &nbsp;
        <b-autocomplete v-model="searchLog"
                        placeholder="Search a log"
                        icon="search"
                        :data="filteredData"
                        @select="option => filtered = option">
          <template slot="empty">
            No results found
          </template>
        </b-autocomplete>
        <p class='control'>
          <button class='button' style="color:#686868" @click="searchLog = ''">
            <b-icon icon="window-close"></b-icon>
          </button>
        </p>
      </b-field>
      <div>
        <div class="control">
          <textarea class="textarea" style="font-family:'Courier New'" readonly rows="40">{{ this.filteredLogs }}</textarea>
        </div>
      </div>
    </template>
  </div>
</template>

<script>
  export default {
    beforeDestroy () {
      this.$options.sockets.onmessage = null;
      this.logs = [];
    },

    created () {
      try {
        this.getLogs();
        this.$options.sockets.onmessage = this.handler;
      } catch ( resp ) {
        if ( resp.status == 501)  {
          this.disabled = true
        } else {
          this.$buefy.toast.open({
            message: 'Getting logs for the past ' + this.duration + ' failed with ' + resp.status + ' status.',
            type: 'is-danger',
            duration: 4000
          });
        }
      }
    },
    
    computed: {
      filteredLogs: function() {
        let logs = this.logs;
        let dur = this.getSeconds(this.duration);
        let now = Date.now() / 1000;

        let windowed = [];

        for ( let i in logs ) {
          let log = logs[ i ];

          if ( log.epoch >= (now - dur) ) {
            windowed.push(log);
          }
        }

        let filters = { 'sources': [], 'levels': [] };

        let tokens = this.searchLog.split( ' ' );

        for ( let i = tokens.length - 1; i >= 0; i-- ) {
          let token = tokens[ i ];

          if ( token.includes( ':' ) ) {
            let filter = token.split( ':' );

            switch ( filter[ 0 ].toLowerCase() ) {
              case 'source': {
                filters[ 'sources' ] = filters[ 'sources' ].concat( filter[ 1 ].split( ',' ).map( f => f.toLowerCase() ) );

                break;
              }

              case 'level': {
                filters[ 'levels' ] = filters[ 'levels' ].concat( filter[ 1 ].split( ',' ).map( f => f.toLowerCase() ) );

                break;
              }
            }

            tokens.splice( i, 1 );
          }
        }
        
        let log_re = new RegExp( tokens.join( ' ' ), 'i' );
        let data = [];
        
        for ( let i in windowed ) {
          let log = windowed[ i ];

          if ( filters[ 'sources' ].length == 0 || filters[ 'sources' ].includes( log.source.toLowerCase() ) ) {
            if ( filters[ 'levels' ].length == 0 || filters[ 'levels' ].includes( log.level.toLowerCase() ) ) {
              if ( log.log.match( log_re ) ) {
                data.push( log );
              }
            }
          }
        }

        let logString = '';

        for ( let i in data ) {
          let entry = data[ i ];
          logString += entry.timestamp + ' ' + entry.source + ' ' + entry.level + ' ' + entry.log + '\n';
        }

        return logString;
      },
      
      filteredData () {
        let logs = this.logs.map( log => { return log.log; } );

        return logs.filter(
          log => log.toString().toLowerCase().indexOf( this.searchLog.toLowerCase() ) >= 0
        )
      },
      
      paginationNeeded () {
        if ( this.logs.length <= 10 ) {
          return false;
        } else {
          return true;
        }
      }
    },

    methods: {
      async getLogs () {
        let resp = await this.$http.get( 'logs?since=' + this.duration );
        let state = await resp.json();

        // Note that sometimes this function gets called more
        // than once, and sometimes `state` ends up being
        // null, perhaps due to multipart responses?
        if ( state ) {
          this.logs.push( ...state.logs );
        }
      },

      handler ( event ) {
        event.data.split( /\r?\n/ ).forEach( m => {
          let msg = JSON.parse( m );
          this.handle( msg );
        });
      },
    
      handle ( msg ) {     
        if ( msg.resource.type == 'log' ) {
          this.logs.push( msg.result );
        }
      },

      assignDuration ( value ) {
        this.duration = value;

        this.getLogs();
      },
    
      decorator ( severity ) {
      // severity low -> high
      // debug, info, warn, error, fatal
      
        if ( severity == "ERROR" || severity == "FATAL" ) {
          return 'is-danger';
        } else if ( severity == "WARN" ) {
          return 'is-warning';
        } else if ( severity == "INFO" ) {
          return 'is-info';
        } else {
          return 'is-primary';
        }
      },

      getSeconds ( str ) {
        let seconds = 0;
        let months = str.match(/(\d+)\s*M/);
        let days = str.match(/(\d+)\s*D/);
        let hours = str.match(/(\d+)\s*h/);
        let minutes = str.match(/(\d+)\s*m/);
        let secs = str.match(/(\d+)\s*s/);

        if (months) { seconds += parseInt(months[1])*86400*30; }
        if (days) { seconds += parseInt(days[1])*86400; }
        if (hours) { seconds += parseInt(hours[1])*3600; }
        if (minutes) { seconds += parseInt(minutes[1])*60; }
        if (secs) { seconds += parseInt(secs[1]); }

        return seconds;
      }
    },
    
    data () {
      return {
        table: {
          striped: true,
          isPaginated: true,
          isPaginationSimple: true,
          paginationSize: 'is-small',
          defaultSortDirection: 'desc',
          currentPage: 1,
          perPage: 10
        },
        disabled: false,
        logs: [],
        searchLog: '',
        duration: '5m'
      }
    }
  }
</script>

<style scoped>
  div.autocomplete >>> a.dropdown-item {
    color: #383838 !important;
  }

  .textarea {
    background-color: #383838;
    color: whitesmoke;
    font-weight: 600;
  }
</style>
