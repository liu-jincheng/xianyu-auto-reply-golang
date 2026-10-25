import assert from 'node:assert/strict'
import test from 'node:test'
import { chatCardHref, chatCardImage } from './chat_card_links.ts'

test('card actions allow platform navigation and reject executable or disguised URLs', () => {
  assert.equal(chatCardHref('https://www.goofish.com/item?id=123'), 'https://www.goofish.com/item?id=123')
  for (const value of ['javascript:alert(1)', 'data:text/html,test', 'https://goofish.com.attacker.example/', 'https://user:pass@www.goofish.com/item', 'fleamarket://confirm-order?id=123']) assert.equal(chatCardHref(value), undefined)
  assert.equal(chatCardImage('//img.alicdn.com/item.jpg'), 'https://img.alicdn.com/item.jpg')
  assert.equal(chatCardImage('javascript:alert(1)'), undefined)
})
