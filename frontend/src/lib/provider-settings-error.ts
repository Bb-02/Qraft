export interface ProviderConnectionFailureSummary {
  message: string;
  details: string;
}

// Interpret only the connection check's own status, never numbers or protocol
// names quoted in a provider response. The caller redacts secrets first.
export function providerConnectionFailureSummary(message: string, baseURL: string): ProviderConnectionFailureSummary | undefined {
  const detail = message.trim();
  const response = /^provider connection check:\s*(openai-chat|openai-responses|anthropic-messages|anthropic|gemini-native)?\s*api error\s*\(status(?:\s*=\s*|\s+)(\d{3})(?:[,\s)]|$)/i.exec(detail);
  if (!response || response[2] !== '404') return undefined;

  let anthropicPath = false;
  try { anthropicPath = new URL(baseURL).pathname.split('/').some(part => part.toLowerCase() === 'anthropic'); } catch { /* The form validates URLs separately. */ }
  const usedOpenAI = response[1]?.toLowerCase().startsWith('openai-');
  return {
    message: anthropicPath && usedOpenAI
      ? '连接检查使用的协议与地址不匹配（HTTP 404）。地址指定了 Anthropic 接口，但服务端使用了 OpenAI 协议。请更新服务端后重试保存；本次配置尚未保存。'
      : '模型服务未找到请求接口或模型（HTTP 404）。请核对 Base URL、接口协议与模型名称后重试；本次配置尚未保存。',
    details: detail,
  };
}
