import { ExternalLink, Package, FileText, MessageSquare } from 'lucide-react'
import type { ChatCard } from '@/api/chatNew'
import { chatCardHref, chatCardImage } from './chat_card_links'

export function ChatMessageCard({ card, isSelf }: { card: ChatCard; isSelf: boolean }) {
  const href = chatCardHref(card.url)
  const image = chatCardImage(card.image)
  const label = card.kind === 'item' ? '商品卡片' : card.kind === 'order' ? '交易提醒' : '闲鱼消息'
  const Icon = card.kind === 'item' ? Package : card.kind === 'order' ? FileText : MessageSquare
  const price = card.price && /^\d/.test(card.price) ? `¥${card.price}` : card.price
  return (
    <article aria-label={label} className={`w-64 max-w-full overflow-hidden rounded-xl border bg-white text-gray-800 shadow-sm dark:bg-gray-800 dark:text-gray-100 ${isSelf ? 'border-blue-200 dark:border-blue-700' : 'border-gray-200 dark:border-gray-600'}`}>
      <div className="flex items-center gap-1.5 border-b border-gray-100 bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
        <Icon className="h-3.5 w-3.5 shrink-0" />{label}
        {card.tag && card.tag !== label && <span className="ml-auto truncate text-blue-600 dark:text-blue-400">{card.tag}</span>}
      </div>
      <div className="p-3">
        <div className="flex items-start gap-3">
          {image && <img src={image} alt="卡片图片" loading="lazy" className="h-16 w-16 shrink-0 rounded-lg object-cover bg-gray-100" onError={(event) => { event.currentTarget.hidden = true }} />}
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium leading-5 break-words">{card.title || '闲鱼卡片'}</div>
            {price && <div className="mt-1 text-base font-semibold text-red-500">{price}</div>}
          </div>
        </div>
        {card.text && <p className="mt-2 whitespace-pre-wrap break-words text-xs leading-5 text-gray-600 dark:text-gray-300">{card.text}</p>}
        {!!card.fields?.length && <dl className="mt-2 space-y-1 text-xs leading-5">
          {card.fields.map((field, index) => <div key={index} className="flex gap-2"><dt className="shrink-0 text-gray-400">{field.label}</dt><dd className="min-w-0 break-all">{field.value}</dd></div>)}
        </dl>}
      </div>
      {href && <a href={href} target="_blank" rel="noopener noreferrer" className="flex items-center justify-between border-t border-gray-100 px-3 py-2.5 text-xs text-blue-600 hover:bg-blue-50 dark:border-gray-700 dark:text-blue-400 dark:hover:bg-gray-700">
        在闲鱼查看详情<ExternalLink className="h-3.5 w-3.5" />
      </a>}
    </article>
  )
}
