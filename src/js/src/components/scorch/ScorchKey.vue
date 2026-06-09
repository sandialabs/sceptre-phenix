<template>
  <div>
    <div class="columns is-vcentered">
      <div class="column" />
      <div class="column is-one-fifth">
        <div class="columns is-variable is-1">
          <div class="column has-text-right">
            <svg width="30" height="30">
              <g transform="translate(15,15)">
                <g class="node">
                  <circle
                    cx="0"
                    cy="0"
                    r="12"
                    class="circle-bg unknown"></circle>
                </g>
              </g>
            </svg>
          </div>
          <div class="column">
            <span style="color: whitesmoke">Uninitialized</span>
          </div>
        </div>
      </div>
      <div class="column is-one-fifth">
        <div class="columns is-variable is-1">
          <div class="column has-text-right">
            <svg width="30" height="30">
              <g transform="translate(15,15)">
                <g class="node">
                  <circle
                    cx="0"
                    cy="0"
                    r="12"
                    class="circle-bg running"></circle>
                  <g class="result-status-glyph running">
                    <path
                      transform="scale(0.03 0.03) translate(-514,-510)"
                      d="M604.16 1003.52V898.458A410.317 410.317 0 0 0 898.458 604.16h105.062a512.614 512.614 0 0 1-399.36 399.36z m-204.8 0A512.614 512.614 0 0 1 0 604.16h105.062A410.317 410.317 0 0 0 399.36 898.458v105.062z m204.8-898.458V0a512.614 512.614 0 0 1 399.36 399.36H898.458A410.317 410.317 0 0 0 604.16 105.062z m-204.8 0A410.317 410.317 0 0 0 105.062 399.36H0A512.614 512.614 0 0 1 399.36 0v105.062zM512 665.6a153.6 153.6 0 1 0 0-307.2 153.6 153.6 0 0 0 0 307.2z m0 102.4a256 256 0 1 1 0-512 256 256 0 0 1 0 512z"
                      p-id="10371"></path>
                  </g>
                </g>
              </g>
            </svg>
          </div>
          <div class="column">
            <span style="color: whitesmoke">Running</span>
          </div>
        </div>
      </div>
      <div class="column is-one-fifth">
        <div class="columns is-variable is-1">
          <div class="column has-text-right">
            <svg width="30" height="30">
              <g transform="translate(15,15)">
                <g class="node">
                  <circle
                    cx="0"
                    cy="0"
                    r="12"
                    class="circle-bg success"></circle>
                  <g class="result-status-glyph">
                    <polygon
                      fill="white"
                      points="-2.00 2.80 -4.80 0.00 -5.73 0.933 -2.00 4.67 6.00 -3.33 5.07 -4.27"></polygon>
                  </g>
                </g>
              </g>
            </svg>
          </div>
          <div class="column">
            <span style="color: whitesmoke">Success</span>
          </div>
        </div>
      </div>
      <div class="column" />
    </div>
    <div class="columns is-vcentered">
      <div class="column" />
      <div class="column is-one-fifth">
        <div class="columns is-variable is-1">
          <div class="column has-text-right">
            <svg width="30" height="30">
              <g transform="translate(15,15)">
                <g class="node">
                  <circle
                    cx="0"
                    cy="0"
                    r="12"
                    class="circle-bg unstable"></circle>
                  <g class="result-status-glyph">
                    <polygon points="-1 -5 1 -5 1 1 -1 1" />
                    <polygon points="-1 3 1 3 1 5 -1 5" />
                  </g>
                </g>
              </g>
            </svg>
          </div>
          <div class="column">
            <span style="color: whitesmoke">Break Point</span>
          </div>
        </div>
      </div>
      <div class="column is-one-fifth">
        <div class="columns is-variable is-1">
          <div class="column has-text-right">
            <svg width="30" height="30">
              <g transform="translate(15,15)">
                <g class="node">
                  <circle
                    cx="0"
                    cy="0"
                    r="12"
                    class="circle-bg background"></circle>
                  <g class="result-status-glyph background">
                    <path
                      transform="scale(0.03 0.03) translate(-514,-510)"
                      d="M604.16 1003.52V898.458A410.317 410.317 0 0 0 898.458 604.16h105.062a512.614 512.614 0 0 1-399.36 399.36z m-204.8 0A512.614 512.614 0 0 1 0 604.16h105.062A410.317 410.317 0 0 0 399.36 898.458v105.062z m204.8-898.458V0a512.614 512.614 0 0 1 399.36 399.36H898.458A410.317 410.317 0 0 0 604.16 105.062z m-204.8 0A410.317 410.317 0 0 0 105.062 399.36H0A512.614 512.614 0 0 1 399.36 0v105.062zM512 665.6a153.6 153.6 0 1 0 0-307.2 153.6 153.6 0 0 0 0 307.2z m0 102.4a256 256 0 1 1 0-512 256 256 0 0 1 0 512z"
                      p-id="10371"></path>
                  </g>
                </g>
              </g>
            </svg>
          </div>
          <div class="column">
            <span style="color: whitesmoke">Backgrounded</span>
          </div>
        </div>
      </div>
      <div class="column is-one-fifth">
        <div class="columns is-variable is-1">
          <div class="column has-text-right">
            <svg width="30" height="30">
              <g transform="translate(15,15)">
                <g class="node">
                  <circle
                    cx="0"
                    cy="0"
                    r="12"
                    class="circle-bg failure"></circle>
                  <g class="result-status-glyph">
                    <polygon
                      fill="white"
                      points="4.67 -3.73 3.73 -4.67 0 -0.94 -3.73 -4.67 -4.67 -3.73 -0.94 0 -4.67 3.73 -3.73 4.67 0 0.94 3.73 4.67 4.67 3.73 0.94 0"></polygon>
                  </g>
                </g>
              </g>
            </svg>
          </div>
          <div class="column">
            <span style="color: whitesmoke">Failure</span>
          </div>
        </div>
      </div>
      <div class="column" />
    </div>
  </div>
</template>

<style scoped>
  .node {
    transform: translateZ(0);
  }

  .node > circle {
    stroke: white;
    stroke-width: 2px;
  }

  .node > circle.success {
    fill: #8cc04f;
  }

  .node > circle.failure {
    fill: #d54c53;
  }

  .node > circle.unstable {
    fill: #f6b44b;
  }

  .node > circle.aborted {
    fill: #949393;
  }

  .node > circle.paused {
    fill: #24b0d5;
  }

  .node > circle.unknown {
    fill: #686868;
  }

  .node > circle.running {
    fill: #fff;
  }

  g.running {
    fill: #8ccc4f;
    animation: rotating 2s linear infinite;
    animation-iteration-count: infinite;
  }

  g.background {
    fill: #f6b44b;
    animation: rotating 2s linear infinite;
    animation-iteration-count: infinite;
  }
</style>
