import type { FetchItemsSummaryResponse } from '@/api/items'

export const getItemSyncFeedback = (result: FetchItemsSummaryResponse): {
  type: 'success' | 'error' | 'warning' | 'info'
  message: string
  duration: number
  reload: boolean
} => {
  const total = result.total_count ?? 0
  const saved = result.saved_count ?? 0
  const accounts = result.account_count ?? 0
  const succeeded = result.success_account_count ?? 0
  const failed = result.failed_account_count ?? result.failed_accounts?.length ?? 0
  const hasProgress = saved > 0 || succeeded > 0
  const reload = result.success || hasProgress

  if (failed > 0 || !result.success) {
    const reason = result.failed_accounts?.[0] || result.message || '请稍后重试'
    return {
      type: hasProgress ? 'warning' : 'error',
      message: hasProgress
        ? `商品同步部分成功：${succeeded}/${accounts} 个账号成功，保存 ${saved} 件，${failed} 个账号失败；${reason}`
        : `商品同步失败：${reason}`,
      duration: 8000,
      reload,
    }
  }

  if (accounts === 0) {
    return { type: 'warning', message: '没有可同步的账号，请先添加并启用闲鱼账号', duration: 5000, reload: false }
  }

  if (saved === 0 && total === 0) {
    return { type: 'info', message: `已检查 ${accounts} 个账号，未发现可同步的商品`, duration: 5000, reload }
  }

  return {
    type: 'success',
    message: `成功获取 ${accounts} 个账号商品，共 ${total} 件，保存 ${saved} 件`,
    duration: 3000,
    reload,
  }
}
