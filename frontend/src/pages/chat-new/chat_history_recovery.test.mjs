import assert from 'node:assert/strict'
import test from 'node:test'
import { CHAT_CONTENT_VERSION, CHAT_HISTORY_SOURCE, needsMessageContentUpgrade, recoverMessages } from './chat_history_recovery.ts'

const message = (messageId,time,text='普通消息') => ({messageId,time,text,type:'system',isSelf:false,senderId:'peer',senderName:'对方',images:[]})
test('card upgrade passes newer cached text and repairs the older placeholder before stopping', async () => {
  const old=message('card',2000000001000,'[其他消息]')
  const boundary={cache:{source:CHAT_HISTORY_SOURCE,msgs:[message('known',2000000003000),old],hasMore:true,cursor:1999999999000},nextCursor:null}
  assert.equal(needsMessageContentUpgrade(boundary.cache),true)
  const cursors=[]
  const result=await recoverMessages(async(cursor)=>{
    cursors.push(cursor)
    if(cursor===undefined)return {items:[message('known',2000000003000)],hasMore:true,nextCursor:2000000002000}
    return {items:[{...old,type:'card',text:'商品',contentVersion:CHAT_CONTENT_VERSION,card:{kind:'item',title:'商品'}}],hasMore:true,nextCursor:2000000000000}
  },boundary,()=>true)
  assert.deepEqual(cursors,[undefined,2000000002000])
  assert.equal(result.complete,true)
  assert.equal(result.items.find((message)=>message.messageId==='card').card.title,'商品')
  assert.equal(result.cursor,1999999999000)
  assert.equal(needsMessageContentUpgrade({...boundary.cache,contentVersion:CHAT_CONTENT_VERSION}),false)
})

test('ordinary cached messages do not trigger content migration',()=>{
  assert.equal(needsMessageContentUpgrade({source:CHAT_HISTORY_SOURCE,msgs:[message('text',2000000001000)],hasMore:false,cursor:null}),false)
})
