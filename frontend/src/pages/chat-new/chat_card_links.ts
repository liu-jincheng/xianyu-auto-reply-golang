export function chatCardHref(raw?: string): string | undefined {
  if (!raw) return undefined
  try {
    const url = new URL(raw.startsWith('//') ? `https:${raw}` : raw)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return undefined
    const host = url.hostname.toLowerCase()
    if (!['goofish.com', 'taobao.com', 'tb.cn', 'xianyu.mobi'].some((domain) => host === domain || host.endsWith(`.${domain}`))) return undefined
    return url.href
  } catch { return undefined }
}

export function chatCardImage(raw?: string): string | undefined {
  if (!raw) return undefined
  try {
    const url = new URL(raw.startsWith('//') ? `https:${raw}` : raw)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : undefined
  } catch { return undefined }
}
