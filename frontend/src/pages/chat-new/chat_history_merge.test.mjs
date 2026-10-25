import assert from 'node:assert/strict'
import test from 'node:test'
import { mergeConversations, mergeMessages, repairConversationSummary } from './chat_history_merge.ts'

const conversation = (cid, date, seconds = false) => ({
  cid, rawCid: cid, otherUserId: cid, otherUserName: cid, otherUserAvatar: '', itemTitle: '',
  lastMessageSummary: '', lastMessageTime: Date.parse(date) / (seconds ? 1000 : 1), unreadCount: 0,
})

test('conversations sort by the full timestamp across years and seconds/milliseconds', () => {
  const oldest = conversation('2024', '2024-12-31T23:59:59+08:00')
  const middle = conversation('2025', '2025-11-30T12:00:00+08:00', true)
  const newest = conversation('2026', '2026-01-01T00:00:01+08:00')
  assert.deepEqual(mergeConversations([oldest, middle, newest], []).map((c) => c.cid), ['2026', '2025', '2024'])
  assert.deepEqual(mergeConversations([oldest], [newest, middle]).map((c) => c.cid), ['2026', '2025', '2024'])
})

test('late history does not replace a newer live summary or duplicate the conversation', () => {
  const older = conversation('same', '2024-12-31T23:59:59+08:00')
  const latest = { ...older, lastMessageTime: Date.parse('2026-01-01T00:00:01+08:00'), lastMessageSummary: '新消息' }
  assert.deepEqual(mergeConversations([older], [latest]), [latest])
})

test('messages remain chronological across years and distinct IDs are preserved', () => {
  const message = (id, date) => ({ messageId: id, senderId: '', senderName: '', isSelf: false,
    type: 'text', text: '相同内容', images: [], time: Date.parse(date) })
  const older = message('old', '2025-12-31T23:59:59+08:00')
  const newer = message('new', '2026-01-01T00:00:01+08:00')
  assert.deepEqual(mergeMessages([newer], [older, newer]), [older, newer])
})

test('current IM summaries retain cached peer profiles when the RPC omits them', () => {
  const old = { ...conversation('same', '2025-06-22T12:00:00+08:00'), otherUserName: '对方', otherUserAvatar: 'avatar.jpg' }
  const fresh = { ...old, otherUserName: '', otherUserAvatar: '', lastMessageTime: Date.parse('2026-10-25T12:00:00+08:00') }
  const [merged] = mergeConversations([fresh], [old])
  assert.equal(merged.otherUserName, '对方')
  assert.equal(merged.otherUserAvatar, 'avatar.jpg')
  assert.equal(merged.lastMessageTime, fresh.lastMessageTime)
})

test('an old unsupported conversation summary is repaired from already loaded messages',()=>{
 const conv={...conversation('same','2026-10-02T12:00:00+08:00'),lastMessageSummary:'[其他消息]'}
 const last={messageId:'card',time:conv.lastMessageTime,type:'card',text:'商品',card:{title:'商品卡片标题'}}
 assert.equal(repairConversationSummary([conv],'same',[last])[0].lastMessageSummary,'商品卡片标题')
 assert.equal(repairConversationSummary([{...conv,lastMessageTime:conv.lastMessageTime+1000}],'same',[last])[0].lastMessageSummary,'[其他消息]')
})
