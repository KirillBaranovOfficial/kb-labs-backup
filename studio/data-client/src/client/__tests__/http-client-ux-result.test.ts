import axios from 'axios';
import { afterEach, describe, expect, it } from 'vitest';
import validSuccess from '../../../../../contracts/ux-result/fixtures/valid/success.json';
import validFailure from '../../../../../contracts/ux-result/fixtures/valid/failure.json';
import invalidFailure from '../../../../../contracts/ux-result/fixtures/invalid/failure-missing-hint.json';
import invalidMixedEnvelope from '../../../../../contracts/ux-result/fixtures/invalid/mixed-success-and-error.json';
import invalidSuccess from '../../../../../contracts/ux-result/fixtures/invalid/success-missing-data.json';
import { gatewayBaseFromApiBase, HttpClient } from '../http-client.js';

const originalAdapter = axios.defaults.adapter;

afterEach(() => {
  axios.defaults.adapter = originalAdapter;
});

function respondWith(data: unknown, status = 200): void {
  axios.defaults.adapter = async (config) => ({
    data,
    status,
    statusText: status === 200 ? 'OK' : 'Service Unavailable',
    headers: {},
    config,
  });
}

describe('Studio API client UX result envelope', () => {
  it('derives a gateway base from the versioned API base for streaming routes', () => {
    expect(gatewayBaseFromApiBase('/api/v1')).toBe('');
    expect(gatewayBaseFromApiBase('https://gateway.test/api/v1')).toBe('https://gateway.test');
    expect(gatewayBaseFromApiBase('https://gateway.test/kb/api/v1')).toBe('https://gateway.test/kb');
  });
  // case: S-203.5
  it('unwraps the shared valid success fixture', async () => {
    respondWith(validSuccess);

    await expect(new HttpClient().fetch('/fixture')).resolves.toEqual(validSuccess.data);
  });

  it('decodes a success envelope for routes that require the UX result contract', async () => {
    respondWith(validSuccess);

    await expect(new HttpClient().fetchUx('/fixture')).resolves.toEqual(validSuccess.data);
  });

  // case: S-203.1
  it('rejects a raw success body for a route that requires the UX envelope', async () => {
    respondWith({ status: 'healthy' });

    await expect(new HttpClient().fetchUx('/health')).rejects.toMatchObject({
      code: 'KB_RUNTIME_UNEXPECTED',
      correlationId: expect.any(String),
    });
  });

  // case: S-203.1
  it('rejects a raw not-ready error body for a route that requires the UX envelope', async () => {
    respondWith({ ready: false, reason: 'upstream unavailable' }, 503);

    await expect(new HttpClient().fetchUx('/ready')).rejects.toMatchObject({
      code: 'KB_RUNTIME_UNEXPECTED',
      status: 503,
      correlationId: expect.any(String),
    });
  });

  // case: S-203.5
  it('preserves the typed shared error fields from a valid failure fixture', async () => {
    respondWith(validFailure);

    await expect(new HttpClient().fetch('/fixture')).rejects.toMatchObject({
      code: validFailure.error.code,
      message: validFailure.error.message,
      hint: validFailure.error.hint,
      correlationId: validFailure.error.correlationId,
    });
  });

  // case: S-005.5
  it('maps an authorization denial from the shared envelope without legacy permission fields', async () => {
    respondWith({ ok: false, error: {
      code: 'KB_AUTH_FORBIDDEN', area: 'auth', stage: 'run', severity: 'error', retryable: false,
      message: 'Permission denied: users:read', hint: 'Ask your administrator for the permission needed to perform this operation.',
      details: { permission: 'users:read' },
    } });

    await expect(new HttpClient().fetch('/api/v1/auth/users')).rejects.toMatchObject({
      code: 'KB_AUTH_FORBIDDEN', message: 'Permission denied: users:read',
      hint: 'Ask your administrator for the permission needed to perform this operation.',
    });
  });

  // case: S-203.5
  it('maps each invalid shared fixture to a safe typed error', async () => {
    for (const fixture of [invalidFailure, invalidMixedEnvelope, invalidSuccess]) {
      respondWith(fixture);
      await expect(new HttpClient().fetch('/fixture')).rejects.toMatchObject({
        code: 'KB_RUNTIME_UNEXPECTED',
        message: 'The operation failed unexpectedly.',
        hint: 'Copy diagnostics and contact support.',
        correlationId: expect.any(String),
      });
    }
    expect(invalidFailure.error.correlationId).toBe('corr-203-3');
  });
});
