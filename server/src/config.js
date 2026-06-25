export function loadConfig(env = process.env) {
  const accessKey = env.ACCESS_KEY;
  if (!accessKey) {
    throw new Error("ACCESS_KEY environment variable is required");
  }
  const apiTimeoutMs = env.API_TIMEOUT_MS
    ? Number(env.API_TIMEOUT_MS)
    : 600000;
  if (!Number.isFinite(apiTimeoutMs) || apiTimeoutMs <= 0) {
    throw new Error("API_TIMEOUT_MS must be a positive number");
  }
  return {
    accessKey,
    anthropicApiKey: env.ANTHROPIC_API_KEY || undefined,
    anthropicAuthToken: env.ANTHROPIC_AUTH_TOKEN || undefined,
    anthropicBaseUrl: env.ANTHROPIC_BASE_URL || undefined,
    httpProxy: env.HTTP_PROXY || undefined,
    httpsProxy: env.HTTPS_PROXY || undefined,
    allProxy: env.ALL_PROXY || undefined,
    noProxy: env.NO_PROXY || "localhost,127.0.0.1",
    apiTimeoutMs,
  };
}
