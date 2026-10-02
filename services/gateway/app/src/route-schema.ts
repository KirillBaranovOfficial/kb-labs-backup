const noBodySchema = {
  anyOf: [
    { type: 'object', maxProperties: 0, additionalProperties: false },
    { type: 'null' },
  ],
} as const;

/** A route body that may be omitted on requests handled through a cookie. */
export function optionalRequestBodySchema<const Body extends Record<string, unknown>>(body: Body) {
  return { anyOf: [body, noBodySchema] } as const;
}
