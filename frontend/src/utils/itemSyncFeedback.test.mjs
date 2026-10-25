import assert from 'node:assert/strict'
import test from 'node:test'
import { getItemSyncFeedback } from './itemSyncFeedback.ts'

test('failed sync displays the actual account error instead of the old completion message', () => {
  const result = getItemSyncFeedback({
    success: false, message: '商品同步完成', account_count: 1,
    failed_account_count: 1, failed_accounts: ['a: 无权限访问'], saved_count: 0,
  })
  assert.equal(result.type, 'error')
  assert.match(result.message, /商品同步失败.*无权限访问/)
  assert.doesNotMatch(result.message, /同步完成/)
  assert.equal(result.reload, false)
})

test('partial success refreshes saved products even when success is false', () => {
  for (const successAccounts of [0, 1]) {
    const result = getItemSyncFeedback({
      success: false, account_count: 2, success_account_count: successAccounts,
      failed_account_count: 1, failed_accounts: ['a: 第二页失败'], saved_count: 6,
    })
    assert.equal(result.type, 'warning')
    assert.match(result.message, /保存 6 件.*第二页失败/)
    assert.equal(result.reload, true)
  }
})

test('successful sync shows saved counts and refreshes the list', () => {
  const result = getItemSyncFeedback({ success: true, account_count: 1, total_count: 6, saved_count: 6 })
  assert.equal(result.type, 'success')
  assert.match(result.message, /共 6 件，保存 6 件/)
  assert.equal(result.reload, true)
})

test('empty accounts and empty product lists do not claim products were fetched', () => {
  const noAccounts = getItemSyncFeedback({ success: false, message: '没有可同步的账号', account_count: 0 })
  assert.match(noAccounts.message, /没有可同步的账号/)
  assert.equal(noAccounts.reload, false)
  const empty = getItemSyncFeedback({ success: true, account_count: 1, saved_count: 0, total_count: 0 })
  assert.equal(empty.type, 'info')
  assert.match(empty.message, /未发现可同步的商品/)
})
