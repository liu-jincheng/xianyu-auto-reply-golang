import assert from 'node:assert/strict'
import test from 'node:test'
import { restoreChatHistory } from './chat_history_storage.ts'
import { CHAT_HISTORY_SOURCE } from './chat_history_recovery.ts'

const snapshot = () => ({ version: 1, activeAccountId: 'a',
  conversations: { a: { convs: [], hasMore: true, cursor: 20250622184017 } },
  messages: { a: { cid: { msgs: [], hasMore: true, cursor: 5000 } } },
  activeConversations: { a: 'cid' }, users: {}, blacklist: {},
  recovery: { conversations: [['a', { cache: { convs: [] }, nextCursor: 20250622184017 }]], messages: [] },
})

test('old cached history is retained but legacy cursors cannot enter current IM queries', () => {
  const value = snapshot()
  const old = { cid: 'old', otherUserId: 'peer', otherUserName: '对方', lastMessageTime: Date.parse('2025-06-22T18:40:17+08:00') }
  value.conversations.a.convs = [old]
  const restored = restoreChatHistory(value)
  assert.equal(restored.activeAccountId, 'a')
  assert.equal(restored.activeConversations.a, 'cid')
  assert.equal(restored.conversations.a.convs[0].otherUserName, '对方')
  assert.equal(restored.conversations.a.cursor, null)
  assert.equal(restored.messages.a.cid.cursor, null)
  assert.equal(restored.conversations.a.source, undefined)
  assert.deepEqual(restored.recovery, { conversations: [], messages: [] })
})

test('current IM cache retains its source and pagination without forcing a reload', () => {
  const value = snapshot()
  value.source = CHAT_HISTORY_SOURCE
  value.conversations.a.source = CHAT_HISTORY_SOURCE
  value.messages.a.cid.source = CHAT_HISTORY_SOURCE
  value.conversations.a.cursor = 1750590000000
  value.messages.a.cid.cursor = 1750589999999
  const restored = restoreChatHistory(value)
  assert.equal(restored.conversations.a.cursor, 1750590000000)
  assert.equal(restored.messages.a.cid.cursor, 1750589999999)
  assert.equal(restored.recovery.conversations.length, 1)
})
