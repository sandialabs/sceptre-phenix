// The server's health endpoints, through the API alone (no browser page). The
// CI backend runs with auth disabled, a BoltDB store and no minimega, so it is
// alive but not ready.
const { test, expect } = require('@playwright/test');

const HEALTH_JSON = 'application/health+json';

// With auth enabled, unauthenticated probes get no output fields.
const authDisabled = !process.env.E2E_AUTH_MODE;

test('/healthz reports liveness', async ({ request }) => {
  const resp = await request.get('/healthz');
  expect(resp.status()).toBe(200);
  expect(resp.headers()['content-type']).toBe(HEALTH_JSON);
  expect(resp.headers()['cache-control']).toBe('no-store');

  const body = await resp.json();
  expect(body.status).toBe('pass');
  expect(body.serviceId).toBe('phenix');
  expect(body.checks.uptime).toHaveLength(1);
  expect(body.checks['store:responseTime']).toHaveLength(1);
  expect(body.checks['store:responseTime'][0].componentId).toBe('bolt');
});

test('/readyz fails without minimega', async ({ request }) => {
  const resp = await request.get('/readyz');
  expect(resp.status()).toBe(503);
  expect(resp.headers()['content-type']).toBe(HEALTH_JSON);

  const body = await resp.json();
  expect(body.status).toBe('fail');

  const minimega = body.checks['minimega:responseTime'];
  expect(minimega).toHaveLength(1);
  expect(minimega[0].status).toBe('fail');
  expect(minimega[0]).not.toHaveProperty('observedValue');

  if (authDisabled) {
    expect(typeof minimega[0].output).toBe('string');
  }

  expect(body.checks.uptime[0].status).toBe('pass');
});

test('/readyz?exclude= leaves checks out', async ({ request }) => {
  const resp = await request.get('/readyz?exclude=minimega&exclude=disk');
  const body = await resp.json();
  expect(body.checks).not.toHaveProperty('minimega:responseTime');
  expect(body.checks).not.toHaveProperty('disk:utilization');
  expect(body.checks).toHaveProperty('uptime');
});

test('HEAD /healthz has no body', async ({ request }) => {
  const resp = await request.head('/healthz');
  expect(resp.status()).toBe(200);
  expect(resp.headers()['content-type']).toBe(HEALTH_JSON);
  expect((await resp.body()).length).toBe(0);
});
