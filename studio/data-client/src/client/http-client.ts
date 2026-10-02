import axios, { type AxiosInstance, type AxiosRequestConfig, type AxiosResponse, type AxiosError } from 'axios';
import type { RequestInterceptor, ResponseInterceptor, ErrorInterceptor } from './types';
import { KBError } from '../errors/kb-error';
import { mapFetchError, mapInvalidResultEnvelope } from './error-mapper';
import { ulid } from 'ulid';

export class HttpClient {
  private client: AxiosInstance;
  private requestInterceptors: RequestInterceptor[] = [];
  private responseInterceptors: ResponseInterceptor[] = [];
  private errorInterceptors: ErrorInterceptor[] = [];
  private baseUrl: string;

  constructor(baseUrl: string = '', token?: string) {
    this.baseUrl = baseUrl;
    this.client = axios.create({
      baseURL: baseUrl,
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
    });

    // Axios request interceptor: add X-Request-Id
    this.client.interceptors.request.use((config) => {
      if (!config.headers['X-Request-Id']) {
        config.headers['X-Request-Id'] = ulid();
      }
      return config;
    });

    // Axios response interceptor: unwrap envelope
    this.client.interceptors.response.use(
      (response) => {
        const data = response.data;

        // Check if response is an envelope
        if (isRecord(data) && 'ok' in data) {
          if (isValidSuccessEnvelope(data)) {
            // Success envelope: unwrap and return data
            response.data = data.data;
          } else if (isValidFailureEnvelope(data)) {
            // Error envelope: throw error
            const error = mapFetchError(data, {
              status: response.status,
              statusText: response.statusText,
            } as Response);
            return Promise.reject(error);
          } else {
            return Promise.reject(mapInvalidResultEnvelope(data, response.status));
          }
        }

        return response;
      },
      (error: AxiosError) => {
        // Transform axios error to KBError
        const body = error.response?.data;
        const status = error.response?.status ?? 500;
        const kbError = isRecord(body) && 'ok' in body && !isValidResultEnvelope(body)
          ? mapInvalidResultEnvelope(body, status)
          : mapFetchError(body, {
          status,
          statusText: error.response?.statusText ?? 'Unknown Error',
        } as Response);
        return Promise.reject(kbError);
      }
    );
  }

  addRequestInterceptor(interceptor: RequestInterceptor): void {
    this.requestInterceptors.push(interceptor);
  }

  addResponseInterceptor(interceptor: ResponseInterceptor): void {
    this.responseInterceptors.push(interceptor);
  }

  addErrorInterceptor(interceptor: ErrorInterceptor): void {
    this.errorInterceptors.push(interceptor);
  }

  getBaseUrl(): string {
    return this.baseUrl;
  }

  async fetch<T>(path: string, options: AxiosRequestConfig = {}): Promise<T> {
    try {
      // Apply custom request interceptors (if any)
      const config: AxiosRequestConfig = {
        url: path,
        ...options,
      };

      // Note: Custom interceptors work with FetchOptions (headers as Headers object)
      // but axios uses plain objects. If you need custom interceptors, adapt them here.

      const response: AxiosResponse<T> = await this.client.request<T>(config);

      // Apply custom response interceptors (if any)
      // Note: These expect Response objects from fetch API
      // You may need to adapt or remove these if not used

      return response.data;
    } catch (error) {
      const kbError = error instanceof KBError ? error : mapFetchError(error);
      throw await this.processError(kbError);
    }
  }

  private async processError(error: KBError): Promise<never> {
    let processedError = error;
    for (const interceptor of this.errorInterceptors) {
      processedError = await interceptor(processedError);
    }
    throw processedError;
  }
}

function isValidResultEnvelope(value: Record<string, unknown>): boolean {
  return isValidSuccessEnvelope(value) || isValidFailureEnvelope(value);
}

function isValidSuccessEnvelope(value: Record<string, unknown>): value is Record<string, unknown> & { ok: true; data: unknown } {
  return value.ok === true && 'data' in value &&
    Object.keys(value).every((key) => key === 'ok' || key === 'data' || key === 'meta') &&
    (value.meta === undefined || isRecord(value.meta));
}

function isValidFailureEnvelope(value: Record<string, unknown>): value is Record<string, unknown> & { ok: false; error: Record<string, unknown> } {
  return value.ok === false && isRecord(value.error) &&
    Object.keys(value).every((key) => key === 'ok' || key === 'error') &&
    isValidErrorEnvelope(value.error);
}

function isValidErrorEnvelope(value: Record<string, unknown>): boolean {
  const areas = ['install', 'host', 'auth', 'project', 'config', 'plugin', 'update', 'runtime', 'product'];
  const stages = ['preflight', 'resolve', 'apply', 'verify', 'recover', 'start', 'login', 'add-project', 'run', 'extend', 'update', 'rollback'];
  const keys = ['code', 'area', 'stage', 'severity', 'retryable', 'message', 'cause', 'hint', 'actions', 'docs', 'correlationId', 'details'];
  if (Object.keys(value).some((key) => !keys.includes(key))) { return false; }
  if (typeof value.code !== 'string' || !/^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$/.test(value.code)) { return false; }
  if (typeof value.area !== 'string' || !areas.includes(value.area)) { return false; }
  if (typeof value.stage !== 'string' || !stages.includes(value.stage)) { return false; }
  if (value.severity !== 'error' && value.severity !== 'warning') { return false; }
  if (typeof value.retryable !== 'boolean') { return false; }
  if (typeof value.message !== 'string' || !value.message.length || typeof value.hint !== 'string' || !value.hint.length) { return false; }
  if (['cause', 'docs', 'correlationId'].some((key) => value[key] !== undefined && typeof value[key] !== 'string')) { return false; }
  if (value.details !== undefined && (!isRecord(value.details) || Object.values(value.details).some((entry) => typeof entry !== 'string'))) { return false; }
  if (value.actions !== undefined && (!Array.isArray(value.actions) || value.actions.some((action) =>
    !isRecord(action) || typeof action.id !== 'string' || !action.id.length || typeof action.label !== 'string' || !action.label.length ||
    Object.keys(action).some((key) => !['id', 'label', 'command'].includes(key)) ||
    (action.command !== undefined && typeof action.command !== 'string')))) { return false; }
  return true;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
