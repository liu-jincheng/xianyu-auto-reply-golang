/** 已访问的聊天记录保存在本机 IndexedDB，按登录用户隔离。 */
import type { ConversationCache, MessageCache, RecoveryBoundary } from './chat_history_recovery'
import { CHAT_HISTORY_SOURCE } from './chat_history_recovery.ts'
import { mergeConversations, mergeMessages } from './chat_history_merge.ts'

export interface ChatHistorySnapshot {
  version: 1
  source?: string
  activeAccountId: string
  conversations: Record<string, ConversationCache>
  messages: Record<string, Record<string, MessageCache>>
  activeConversations: Record<string, string>
  users: Record<string, { avatar: string; nick: string }>
  blacklist: Record<string, boolean>
  recovery: {
    conversations: [string, RecoveryBoundary<ConversationCache>][]
    messages: [string, [string, RecoveryBoundary<MessageCache>][]][]
  }
}

let database: Promise<IDBDatabase> | undefined
function openDatabase(): Promise<IDBDatabase> {
  database ??= new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open('xianyu-chat-history', 1)
    request.onupgradeneeded = () => request.result.createObjectStore('users')
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
    request.onblocked = () => reject(new Error('本机聊天缓存被其他页面占用'))
  }).catch((error) => { database = undefined; throw error })
  return database
}

/** 不做定时失效；打开已有聊天不会因此请求平台。 */
export async function loadChatHistory(owner: string): Promise<ChatHistorySnapshot | null> {
  const db = await openDatabase()
  const value = await new Promise<ChatHistorySnapshot | undefined>((resolve, reject) => {
    const request = db.transaction('users').objectStore('users').get(owner)
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
  return restoreChatHistory(value)
}

export function restoreChatHistory(value: ChatHistorySnapshot | undefined): ChatHistorySnapshot | null {
  if (!value || value.version !== 1) return null
  // 原 MTOP 返回的是旧快照；保留内容，但其游标不能用于 IM 长连接分页。
  if (value.source !== CHAT_HISTORY_SOURCE) value.recovery = { conversations: [], messages: [] }
  // 恢复时重新按完整时间排序，兼容旧版本存入的列表顺序。
  for (const cache of Object.values(value.conversations)) {
    cache.convs = mergeConversations(cache.convs, [])
    if (cache.source !== CHAT_HISTORY_SOURCE) cache.cursor = null
  }
  for (const account of Object.values(value.messages)) {
    for (const cache of Object.values(account)) {
      cache.msgs = mergeMessages(cache.msgs, [])
      if (cache.source !== CHAT_HISTORY_SOURCE) cache.cursor = null
    }
  }
  return value
}

export async function saveChatHistory(owner: string, snapshot: ChatHistorySnapshot): Promise<void> {
  // 等待数据库打开期间实时消息仍可能修改 ref，先冻结本次快照。
  const frozen = structuredClone(snapshot)
  const db = await openDatabase()
  await new Promise<void>((resolve, reject) => {
    const transaction = db.transaction('users', 'readwrite')
    transaction.objectStore('users').put(frozen, owner)
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error)
    transaction.onabort = () => reject(transaction.error)
  })
}
