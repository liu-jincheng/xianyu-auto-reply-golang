import { formatChatTime } from '@/utils/date'
/**
 * 在线聊天 主页面
 *
 * 三栏布局：左侧账号列表 | 中间会话列表 | 右侧聊天记录
 * 支持多账号切换，基于WebSocket API获取数据
 */
import { useEffect, useState, useRef, useCallback } from 'react'
import { Loader2, LogIn, LogOut, MessageCircle, RefreshCw, User, ChevronUp, X, Send, AlertCircle, Ban, ImagePlus } from 'lucide-react'
import { useUIStore } from '@/store/uiStore'
import { useAuthStore } from '@/store/authStore'
import {
  getChatAccounts,
  getConnectedChatAccountIds,
  connectAccount,
  disconnectAccount,
  getConversations,
  getMessages,
  sendTextMessage,
  sendImageMessage,
  queryUserInfos,
  getAccountProfile,
  getCustomerOrders,
  getQuickPhrases,
  createQuickPhrase,
  updateQuickPhrase,
  deleteQuickPhrase,
  recallMessage,
  getOfficialBlacklistStatus,
  changeOfficialBlacklist,
  type ChatAccount,
  type Conversation,
  type ChatMessage,
  type CustomerOrder,
  type QuickPhrase,
} from '@/api/chatNew'
import { cancelOrder, fetchXianyuOrders, getOrderDetail, manualDelivery, noLogisticsDelivery, type OrderDetail } from '@/api/orders'
import { useChatNewWs } from './useChatNewWs'
import { appendMsg, mergeConversations, mergeMessages, repairConversationSummary, toTimestampMs } from './chat_history_merge'
import { CHAT_CONTENT_VERSION, CHAT_HISTORY_SOURCE, ChatHistoryRecovery, isLegacyUnsupportedMessage, needsMessageContentUpgrade, recoverConversations, recoverMessages,
  type ConversationCache, type MessageCache } from './chat_history_recovery'
import { loadChatHistory, saveChatHistory } from './chat_history_storage'
import { ConfirmModal } from '@/components/common/ConfirmModal'
import { CustomerOrdersPanel } from './CustomerOrdersPanel'
import { QuickPhrasesPanel } from './QuickPhrasesPanel'
import { OrderDetailModal } from './OrderDetailModal'
import { ChatMessageCard } from './ChatMessageCard'

/** 检查昵称是否为纯数字（如用户ID），纯数字视为无效昵称 */
const isPureDigits = (name: string) => /^\d+$/.test(name)

const canRecallMessage = (message: ChatMessage) =>
  message.isSelf &&
  !!message.messageId &&
  message.type !== 'system' &&
  Date.now() - toTimestampMs(message.time) >= 0 &&
  Date.now() - toTimestampMs(message.time) <= 120_000

export function ChatNew() {
  const { addToast } = useUIStore()
  const cacheOwner = useAuthStore((state) => state.user?.user_id ? String(state.user.user_id) : '')
  const [historyReady, setHistoryReady] = useState(false)
  const [historyRefreshVersion, setHistoryRefreshVersion] = useState(0)

  // 账号相关（分页加载）
  const [accounts, setAccounts] = useState<ChatAccount[]>([])
  const [activeAccountId, setActiveAccountId] = useState('')
  const [loadingAccounts, setLoadingAccounts] = useState(false)
  const [connectingId, setConnectingId] = useState('')
  const [accountPage, setAccountPage] = useState(1)
  const [accountHasMore, setAccountHasMore] = useState(false)
  const accountListRef = useRef<HTMLDivElement>(null)

  // 会话相关
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [loadingConvs, setLoadingConvs] = useState(false)
  const [activeCid, setActiveCid] = useState('')
  const [convHasMore, setConvHasMore] = useState(false)
  const [convCursor, setConvCursor] = useState<number | null>(null)

  // 消息相关
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [loadingMsgs, setLoadingMsgs] = useState(false)
  const [msgHasMore, setMsgHasMore] = useState(false)
  const [msgCursor, setMsgCursor] = useState<number | null>(null)
  const msgContainerRef = useRef<HTMLDivElement>(null)

  // 图片预览
  const [previewImage, setPreviewImage] = useState('')

  // 用户信息缓存（otherUserId -> {avatar, nick}）
  const userInfoCacheRef = useRef<Record<string, { avatar: string; nick: string }>>({})

  // 发送消息
  const [inputText, setInputText] = useState('')
  const [sending, setSending] = useState(false)
  // 发送图片：隐藏的文件选择框引用
  const imageInputRef = useRef<HTMLInputElement>(null)
  const [pendingImage, setPendingImage] = useState<{ file: File; previewUrl: string } | null>(null)
  const pendingImageRef = useRef<{ file: File; previewUrl: string } | null>(null)

  // 当前客户订单与快捷短语
  const [customerOrders, setCustomerOrders] = useState<CustomerOrder[]>([])
  const [loadingOrders, setLoadingOrders] = useState(false)
  const [deliveringOrderNo, setDeliveringOrderNo] = useState('')
  const [confirmingOrderNo, setConfirmingOrderNo] = useState('')
  const [cancellingOrderNo, setCancellingOrderNo] = useState('')
  const [orderDetail, setOrderDetail] = useState<OrderDetail | null>(null)
  const [loadingOrderDetail, setLoadingOrderDetail] = useState(false)
  const [blacklisting, setBlacklisting] = useState(false)
  const [isOfficiallyBlocked, setIsOfficiallyBlocked] = useState(false)
  const [recallingMessageId, setRecallingMessageId] = useState('')
  const [confirmDialog, setConfirmDialog] = useState<{
    title: string
    message: string
    confirmText: string
    type: 'warning' | 'danger' | 'info'
  } | null>(null)
  const confirmResolverRef = useRef<((confirmed: boolean) => void) | null>(null)
  const [quickPhrases, setQuickPhrases] = useState<QuickPhrase[]>([])
  const [editingPhraseId, setEditingPhraseId] = useState<number | null>(null)
  const [phraseTitle, setPhraseTitle] = useState('')
  const [phraseContent, setPhraseContent] = useState('')
  const [savingPhrase, setSavingPhrase] = useState(false)

  // 页面需要实时订阅的账号列表（手动连接成功后加入，刷新后自动恢复已连接账号）
  const [wsAccountIds, setWsAccountIds] = useState<string[]>([])

  // 手机端 Tab 切换（桌面端 md+ 仍为四栏并排，本 state 不影响桌面布局）
  type MobileTab = 'accounts' | 'convs' | 'chat' | 'tools'
  const [mobileTab, setMobileTab] = useState<MobileTab>('accounts')

  const requestConfirm = useCallback((options: {
    title?: string
    message: string
    confirmText?: string
    type?: 'warning' | 'danger' | 'info'
  }) => new Promise<boolean>((resolve) => {
    confirmResolverRef.current?.(false)
    confirmResolverRef.current = resolve
    setConfirmDialog({
      title: options.title || '确认操作',
      message: options.message,
      confirmText: options.confirmText || '确定',
      type: options.type || 'warning',
    })
  }), [])

  const closeConfirm = (confirmed: boolean) => {
    confirmResolverRef.current?.(confirmed)
    confirmResolverRef.current = null
    setConfirmDialog(null)
  }

  // 用 ref 保存当前选中的账号和会话，供 WebSocket 回调使用（避免闭包问题）
  const activeAccountIdRef = useRef(activeAccountId)
  useEffect(() => { activeAccountIdRef.current = activeAccountId }, [activeAccountId])
  const activeCidRef = useRef(activeCid)
  useEffect(() => { activeCidRef.current = activeCid }, [activeCid])
  const reloadOrdersRef = useRef<() => void>(() => {})
  const convCursorRef = useRef<number | null>(null)
  useEffect(() => { convCursorRef.current = convCursor }, [convCursor])
  const msgCursorRef = useRef<number | null>(null)
  useEffect(() => { msgCursorRef.current = msgCursor }, [msgCursor])
  const messagesRef = useRef<ChatMessage[]>([])
  useEffect(() => { messagesRef.current = messages }, [messages])
  const conversationsRef = useRef<Conversation[]>([])
  useEffect(() => { conversationsRef.current = conversations }, [conversations])
  const historyPageRef = useRef({ convHasMore, msgHasMore })
  useEffect(() => { historyPageRef.current = { convHasMore, msgHasMore } }, [convHasMore, msgHasMore])
  const convRequestIdRef = useRef<Record<string, number>>({})
  const convPendingRef = useRef(new Set<string>())
  const msgRequestIdRef = useRef(0)

  /** 同步更新账号 ref，避免账号切换后旧请求把数据写入新账号窗口 */
  const selectAccount = useCallback((accountId: string) => {
    activeAccountIdRef.current = accountId
    setActiveAccountId(accountId)
  }, [])

  /** 同步更新会话 ref，避免会话切换后旧请求把消息写入新会话窗口 */
  const selectCid = useCallback((cid: string) => {
    activeCidRef.current = cid
    setActiveCid(cid)
  }, [])

  // ==================== 按账号缓存：切换账号时保留数据 ====================
  /** 每个账号的会话列表缓存 */
  const convsCacheRef = useRef<Record<string, ConversationCache>>({})
  /** 每个账号每个会话的消息缓存 */
  const msgsCacheRef = useRef<Record<string, Record<string, MessageCache>>>({})
  const recoveryRef = useRef(new ChatHistoryRecovery())
  /** 记住每个账号上次选中的会话 */
  const activeConvPerAccountRef = useRef<Record<string, string>>({})
  const blacklistCacheRef = useRef<Record<string, boolean>>({})
  const rememberedAccountRef = useRef('')
  const historyOwnerRef = useRef('')
  const persistTimerRef = useRef<ReturnType<typeof setTimeout>>()
  const storageErrorShownRef = useRef(false)

  const persistHistory = useCallback(() => {
    if (!cacheOwner || historyOwnerRef.current !== cacheOwner) return
    void saveChatHistory(cacheOwner, {
      version: 1, source: CHAT_HISTORY_SOURCE, activeAccountId: activeAccountIdRef.current,
      conversations: convsCacheRef.current, messages: msgsCacheRef.current,
      activeConversations: activeConvPerAccountRef.current,
      users: userInfoCacheRef.current, blacklist: blacklistCacheRef.current,
      recovery: {
        conversations: [...recoveryRef.current.conversations],
        messages: [...recoveryRef.current.messages].map(([accountId, boundaries]) => [accountId, [...boundaries]]),
      },
    }).catch(() => {
      if (storageErrorShownRef.current) return
      storageErrorShownRef.current = true
      addToast({ message: '本机聊天缓存保存失败，请检查浏览器存储空间', type: 'warning' })
    })
  }, [cacheOwner, addToast])

  const schedulePersist = useCallback(() => {
    clearTimeout(persistTimerRef.current)
    persistTimerRef.current = setTimeout(persistHistory, 250)
  }, [persistHistory])

  useEffect(() => {
    let cancelled = false
    historyOwnerRef.current = ''
    setHistoryReady(false)
    if (!cacheOwner) return
    loadChatHistory(cacheOwner).then((snapshot) => {
      if (cancelled || !snapshot) return
      convsCacheRef.current = snapshot.conversations
      msgsCacheRef.current = snapshot.messages
      activeConvPerAccountRef.current = snapshot.activeConversations
      userInfoCacheRef.current = snapshot.users
      blacklistCacheRef.current = snapshot.blacklist
      rememberedAccountRef.current = snapshot.activeAccountId
      recoveryRef.current.conversations.clear()
      recoveryRef.current.messages.clear()
      for (const [accountId, boundary] of snapshot.recovery.conversations) {
        recoveryRef.current.conversations.set(accountId, boundary)
      }
      for (const [accountId, boundaries] of snapshot.recovery.messages) {
        recoveryRef.current.messages.set(accountId, new Map(boundaries))
      }
    }).catch(() => {
      if (!cancelled) addToast({ message: '本机聊天缓存读取失败，本次将重新获取', type: 'warning' })
    }).finally(() => {
      if (cancelled) return
      historyOwnerRef.current = cacheOwner
      setHistoryReady(true)
    })
    return () => { cancelled = true }
  }, [cacheOwner, addToast])

  useEffect(() => {
    const flush = () => { clearTimeout(persistTimerRef.current); persistHistory() }
    const onHidden = () => { if (document.visibilityState === 'hidden') flush() }
    window.addEventListener('pagehide', flush)
    document.addEventListener('visibilitychange', onHidden)
    return () => {
      flush()
      window.removeEventListener('pagehide', flush)
      document.removeEventListener('visibilitychange', onHidden)
    }
  }, [persistHistory])

  /** 冻结断线前范围；重复断线和后续实时推送不能提前推进补偿边界。 */
  const prepareAccountRecovery = useCallback((accountId: string, disconnected = false) => {
    let conversationCache = convsCacheRef.current[accountId]
    const messageCaches = { ...msgsCacheRef.current[accountId] }
    if (accountId === activeAccountIdRef.current) {
      conversationCache = { ...conversationCache, convs: conversationsRef.current,
        hasMore: historyPageRef.current.convHasMore, cursor: convCursorRef.current }
      if (activeCidRef.current) {
        messageCaches[activeCidRef.current] = { ...messageCaches[activeCidRef.current], msgs: messagesRef.current,
          hasMore: historyPageRef.current.msgHasMore, cursor: msgCursorRef.current }
      }
    }
    recoveryRef.current.prepare(accountId, conversationCache, messageCaches, disconnected)
  }, [])

  /**
   * 同步当前会话列表到缓存
   * 注意：切换账号时 activeAccountId 已变但 conversations 还是旧账号的，
   * 必须跳过这次同步，否则会把旧数据写到新账号的缓存中
   */
  const convSyncAccountRef = useRef('')
  useEffect(() => {
    if (activeAccountId !== convSyncAccountRef.current) {
      convSyncAccountRef.current = activeAccountId
      return // activeAccountId 刚切换，conversations 还是旧的，跳过
    }
    if (activeAccountId && convsCacheRef.current[activeAccountId]) {
      convsCacheRef.current[activeAccountId] = { ...convsCacheRef.current[activeAccountId], convs: conversations, hasMore: convHasMore, cursor: convCursor }
    }
  }, [conversations, convHasMore, convCursor, activeAccountId])
  /** 同步当前消息列表到缓存（同理，切换账号或会话时跳过） */
  const msgSyncKeyRef = useRef('')
  useEffect(() => {
    const key = `${activeAccountId}:${activeCid}`
    if (key !== msgSyncKeyRef.current) {
      msgSyncKeyRef.current = key
      return
    }
    if (activeAccountId && activeCid && msgsCacheRef.current[activeAccountId]?.[activeCid]) {
      if (!msgsCacheRef.current[activeAccountId]) msgsCacheRef.current[activeAccountId] = {}
      msgsCacheRef.current[activeAccountId][activeCid] = { ...msgsCacheRef.current[activeAccountId][activeCid], msgs: messages, hasMore: msgHasMore, cursor: msgCursor }
    }
  }, [messages, msgHasMore, msgCursor, activeAccountId, activeCid])
  /** 记住每个账号的当前会话（同理跳过） */
  const cidSyncAccountRef = useRef('')
  useEffect(() => {
    if (activeAccountId !== cidSyncAccountRef.current) {
      cidSyncAccountRef.current = activeAccountId
      return
    }
    if (activeAccountId) activeConvPerAccountRef.current[activeAccountId] = activeCid
  }, [activeCid, activeAccountId])

  useEffect(() => {
    if (historyReady) schedulePersist()
  }, [conversations, messages, convHasMore, convCursor, msgHasMore, msgCursor,
    activeAccountId, activeCid, historyReady, schedulePersist])

  // ==================== WebSocket 实时推送（多账号） ====================
  /** 更新会话列表的通用逻辑（可作用于 state 数组或缓存数组） */
  const updateConvList = (convs: Conversation[], cid: string, summary: string, msg: ChatMessage, isViewing: boolean): Conversation[] => {
    const exists = convs.some((c) => c.cid === cid)
    if (exists) {
      const updated = convs.map((c) => {
        if (c.cid !== cid) return c
        if (toTimestampMs(msg.time) < toTimestampMs(c.lastMessageTime)) return c
        return { ...c, lastMessageSummary: summary, lastMessageTime: msg.time, unreadCount: isViewing ? 0 : c.unreadCount + 1 }
      })
      return mergeConversations(updated, [])
    }
    // 新会话
    const newConv: Conversation = {
      cid, rawCid: cid,
      otherUserId: msg.isSelf ? '' : msg.senderId,
      otherUserName: msg.isSelf ? '' : (msg.senderName || ''),
      otherUserAvatar: '', itemTitle: '',
      lastMessageSummary: summary, lastMessageTime: msg.time,
      unreadCount: isViewing ? 0 : 1,
    }
    return mergeConversations([newConv], convs)
  }

  const handleWsNewMessage = useCallback((accountId: string, cid: string, msg: ChatMessage) => {
    const summary = msg.type === 'image' ? '[图片]' : (msg.text || '').slice(0, 50)
    const isActiveAccount = accountId === activeAccountIdRef.current
    const isViewingConv = isActiveAccount && cid === activeCidRef.current
    const conversationCache = convsCacheRef.current[accountId]
    if (conversationCache) {
      conversationCache.convs = updateConvList(conversationCache.convs, cid, summary, msg, isViewingConv)
    }
    let messageCache = msgsCacheRef.current[accountId]?.[cid]
    if (!messageCache && isViewingConv) {
      msgsCacheRef.current[accountId] ??= {}
      messageCache = { msgs: messagesRef.current,
        hasMore: historyPageRef.current.msgHasMore, cursor: msgCursorRef.current }
      msgsCacheRef.current[accountId][cid] = messageCache
    }
    // 当前账号的非当前会话也更新缓存；补偿边界保存在独立对象中。
    if (messageCache) messageCache.msgs = appendMsg(messageCache.msgs, msg)
    if (isActiveAccount) {
      const nextConversations = conversationCache?.convs ??
        updateConvList(conversationsRef.current, cid, summary, msg, isViewingConv)
      conversationsRef.current = nextConversations
      setConversations(nextConversations)
      if (isViewingConv && messageCache) {
        messagesRef.current = messageCache.msgs
        setMessages(messageCache.msgs)
        window.setTimeout(() => reloadOrdersRef.current(), 900)
      }
    }
    schedulePersist()
  }, [schedulePersist])

  // WebSocket 断连时刷新账号状态，保留订阅以等待 IM 临时离线后恢复
  // （节流：最多每 5 秒刷一次，避免网络抖动时请求风暴）
  const lastDisconnectRefreshRef = useRef(0)
  const handleWsDisconnect = useCallback((accountId: string) => {
    prepareAccountRecovery(accountId, true)
    schedulePersist()
    const now = Date.now()
    if (now - lastDisconnectRefreshRef.current < 5000) return
    lastDisconnectRefreshRef.current = now
    getChatAccounts(1)
      .then((res) => {
        setAccounts(res.data)
        setAccountPage(1)
        setAccountHasMore(res.hasMore)
      })
      .catch((error: Error) => addToast({ message: error.message, type: 'error' }))
  }, [addToast, prepareAccountRecovery, schedulePersist])

  // 首次订阅只接收实时消息；已有缓存无需随页面进入再次拉取。
  const reloadCurrentChatRef = useRef<(accountId: string) => void>(() => {})
  const handleWsConnected = useCallback((accountId: string, isReconnect: boolean) => {
    setAccounts((prev) => prev.map((account) => (
      account.account_id === accountId ? { ...account, connected: true } : account
    )))
    // 真正断线重连时补齐缺口，避免漏掉离线期间的消息。
    if (isReconnect) {
      prepareAccountRecovery(accountId)
      void reloadCurrentChatRef.current(accountId)
    } else if (historyOwnerRef.current && accountId === activeAccountIdRef.current &&
      convsCacheRef.current[accountId]?.source !== CHAT_HISTORY_SOURCE && !convPendingRef.current.has(accountId)) {
      // 初次进入时 IM 可能尚未就绪，握手成功后只补未初始化或旧接口缓存。
      void reloadCurrentChatRef.current(accountId)
    }
  }, [prepareAccountRecovery])

  // 为当前页面需要实时接收消息的账号建立独立 WebSocket
  useChatNewWs({
    accountIds: wsAccountIds,
    onNewMessage: handleWsNewMessage,
    onConnected: handleWsConnected,
    onDisconnect: handleWsDisconnect,
  })

  // ==================== 加载账号列表（分页） ====================
  const loadAccounts = useCallback(async (page = 1) => {
    setLoadingAccounts(true)
    try {
      const res = await getChatAccounts(page)
      if (page === 1) {
        setAccounts(res.data)
      } else {
        setAccounts((prev) => [...prev, ...res.data])
      }
      setAccountPage(page)
      setAccountHasMore(res.hasMore)
    } catch (e: any) {
      addToast({ message: e.message || '获取账号列表失败', type: 'error' })
    } finally {
      setLoadingAccounts(false)
    }
  }, [addToast])

  /** 加载更多账号 */
  const loadMoreAccounts = useCallback(() => {
    if (!loadingAccounts && accountHasMore) {
      loadAccounts(accountPage + 1)
    }
  }, [loadingAccounts, accountHasMore, accountPage, loadAccounts])

  useEffect(() => {
    loadAccounts()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // 页面刷新后，自动恢复所有已连接账号的实时消息订阅
  useEffect(() => {
    let cancelled = false
    getConnectedChatAccountIds()
      .then((accountIds) => {
        if (cancelled || accountIds.length === 0) return
        setWsAccountIds((prev) => {
          const next = [...prev]
          for (const accountId of accountIds) {
            if (!next.includes(accountId)) next.push(accountId)
          }
          return next.length === prev.length ? prev : next
        })
      })
      .catch((e: any) => {
        if (!cancelled) {
          addToast({ message: e.message || '获取已连接账号失败', type: 'error' })
        }
      })
    return () => {
      cancelled = true
    }
  }, [addToast])

  // ==================== 连接/断开 ====================
  const handleConnect = async (accountId: string) => {
    setConnectingId(accountId)
    try {
      const res = await connectAccount(accountId)
      if (res.success) {
        addToast({ message: '连接成功', type: 'success' })
        await loadAccounts()
        selectAccount(accountId)
        setWsAccountIds((prev) => prev.includes(accountId) ? prev : [...prev, accountId])
        // 手机端：连接成功后自动切到"会话"Tab（桌面端不受影响）
        setMobileTab('convs')
      } else {
        addToast({ message: res.message || '连接失败', type: 'error' })
      }
    } catch (e: any) {
      addToast({ message: e.message || '连接失败', type: 'error' })
    } finally {
      setConnectingId('')
    }
  }

  const handleDisconnect = async (accountId: string) => {
    prepareAccountRecovery(accountId, true)
    try {
      await disconnectAccount(accountId)
      addToast({ message: '已断开连接', type: 'success' })
      setWsAccountIds((prev) => prev.filter((id) => id !== accountId))
      if (activeAccountId === accountId) {
        selectAccount('')
        setConversations([])
        selectCid('')
        setMessages([])
        // 手机端：当前账号被断开后回到"账号"Tab
        setMobileTab('accounts')
      }
      await loadAccounts()
    } catch (e: any) {
      addToast({ message: e.message || '断开失败', type: 'error' })
    }
  }

  /** 点击账号卡片：已连接则选中，未连接则自动连接 */
  const handleSelectAccount = async (acc: ChatAccount) => {
    if (acc.connected) {
      selectAccount(acc.account_id)
      // 选中已连接账号时也建立 WebSocket（页面刷新后首次选中时触发）
      setWsAccountIds((prev) => prev.includes(acc.account_id) ? prev : [...prev, acc.account_id])
      // 手机端：选中已连接账号后切到"会话"Tab
      setMobileTab('convs')
    } else {
      await handleConnect(acc.account_id)
    }
  }

  // ==================== 加载会话列表 ====================
  const loadConversations = useCallback(async (accountId: string, append = false, merge = false) => {
    if (!accountId) return
    convPendingRef.current.add(accountId)
    const requestId = (convRequestIdRef.current[accountId] ?? 0) + 1
    convRequestIdRef.current[accountId] = requestId
    const generation = recoveryRef.current.generation(accountId)
    const isCurrent = () => requestId === convRequestIdRef.current[accountId] &&
      generation === recoveryRef.current.generation(accountId)
    const isActive = () => accountId === activeAccountIdRef.current
    if (isActive()) setLoadingConvs(true)
    const existing = convsCacheRef.current[accountId] ?? { convs: [], hasMore: false, cursor: null }
    let boundary = recoveryRef.current.conversations.get(accountId)
    if (existing.source !== CHAT_HISTORY_SOURCE) {
      recoveryRef.current.conversations.delete(accountId)
      boundary = undefined
    }
    const compensating = existing.source === CHAT_HISTORY_SOURCE && (merge || (append && !!boundary))
    if (compensating) boundary ??= recoveryRef.current.ensureConversations(accountId, existing)

    const fetchPage = async (cursor?: number) => {
      const page = await getConversations(accountId, cursor)
      return { items: page.conversations, hasMore: page.hasMore, nextCursor: page.nextCursor }
    }
    const request = compensating && boundary
      ? recoverConversations(fetchPage, boundary, isCurrent)
      : fetchPage(append && existing.source === CHAT_HISTORY_SOURCE ? existing.cursor ?? undefined : undefined).then((page) => ({
        items: page.items, hasMore: page.hasMore, cursor: page.nextCursor, complete: true,
      }))
    await request.then((result) => {
      if (!result || !isCurrent()) return
      const latest = result.items.map((conversation) => {
        const info = userInfoCacheRef.current[conversation.otherUserId]
        if (!info) return conversation
        return { ...conversation,
          otherUserAvatar: conversation.otherUserAvatar || info.avatar,
          otherUserName: !conversation.otherUserName || isPureDigits(conversation.otherUserName)
            ? info.nick || conversation.otherUserName : conversation.otherUserName }
      })
      const cached = convsCacheRef.current[accountId]
      const next: ConversationCache = {
        source: CHAT_HISTORY_SOURCE,
        convs: mergeConversations(latest, cached?.convs ?? (isActive() ? conversationsRef.current : [])),
        hasMore: result.hasMore, cursor: result.cursor,
      }
      convsCacheRef.current[accountId] = next
      if (result.complete) recoveryRef.current.conversations.delete(accountId)
      else if (boundary) boundary.nextCursor = result.cursor
      schedulePersist()
      if (isActive()) {
        conversationsRef.current = next.convs
        convCursorRef.current = next.cursor
        setConversations(next.convs)
        setConvHasMore(next.hasMore)
        setConvCursor(next.cursor)
      }
      if (!result.complete) {
        addToast({ message: `账号 ${accountId} 会话较多，请点击加载更多继续补齐`, type: 'warning' })
      }
      if (!append && isActive() && latest[0]?.cid) {
        getAccountProfile(accountId, latest[0].cid).then((profile) => {
          if (profile.nick) setAccounts((prev) => prev.map((account) => (
            account.account_id === accountId ? { ...account, display_name: profile.nick } : account
          )))
        }).catch((error: Error) => addToast({ message: error.message, type: 'error' }))
      }
    }).catch((error: Error) => {
      if (isCurrent()) addToast({ message: error.message, type: 'error' })
    }).finally(() => {
      if (requestId === convRequestIdRef.current[accountId]) {
        convPendingRef.current.delete(accountId)
        if (isActive()) setLoadingConvs(false)
      }
    })
  }, [addToast, schedulePersist])

  // 用 ref 保持 accounts 最新引用，避免 effect 闭包取到旧值
  const accountsRef = useRef(accounts)
  useEffect(() => { accountsRef.current = accounts }, [accounts])

  // 缓存读取和本地账号列表均就绪后，恢复上次账号；首次使用选中首个账号。
  useEffect(() => {
    if (!historyReady || activeAccountId || accounts.length === 0) return
    const remembered = accounts.find((account) => account.account_id === rememberedAccountRef.current)
    if (!remembered && rememberedAccountRef.current && accountHasMore) {
      loadMoreAccounts()
      return
    }
    const account = remembered ?? accounts.find((item) => item.connected) ?? accounts[0]
    selectAccount(account.account_id)
  }, [historyReady, activeAccountId, accounts, accountHasMore, loadMoreAccounts, selectAccount])

  // ==================== 加载用户信息（头像+昵称） ====================
  // 找出缺少头像或昵称的会话，构建 [{userId, cid}] 查询列表
  const missingInfoConvs = conversations.filter(
    (c) => {
      if (!c.otherUserId || !c.cid) return false
      // 昵称有效（非空且非纯数字）且头像存在时无需查询
      const hasValidName = !!c.otherUserName && !isPureDigits(c.otherUserName)
      if (c.otherUserAvatar && hasValidName) return false
      // 缓存中已有完整信息则跳过
      const cached = userInfoCacheRef.current[c.otherUserId]
      if (cached && cached.avatar && cached.nick) return false
      return true
    },
  )
  // 以 userId 去重后构建依赖 key
  const missingInfoKey = [...new Set(missingInfoConvs.map((c) => c.otherUserId))].sort().join(',')

  useEffect(() => {
    if (!activeAccountId || !missingInfoKey) return

    // 以 userId 去重后构建查询参数
    const seen = new Set<string>()
    const queries: { userId: string; cid: string }[] = []
    for (const c of missingInfoConvs) {
      if (!seen.has(c.otherUserId)) {
        seen.add(c.otherUserId)
        queries.push({ userId: c.otherUserId, cid: c.cid })
      }
    }
    if (queries.length === 0) return

    // 分批查询（每批3个），每批完成立即更新UI
    const BATCH_SIZE = 3
    let cancelled = false

    const applyInfos = (infos: Record<string, { avatar: string; nick: string }>) => {
      for (const [uid, info] of Object.entries(infos)) {
        // 同时缓存 avatar 和 nick
        const prev = userInfoCacheRef.current[uid] || { avatar: '', nick: '' }
        userInfoCacheRef.current[uid] = {
          avatar: info.avatar || prev.avatar,
          nick: info.nick || prev.nick,
        }
      }
      setConversations((prev) =>
        prev.map((c) => {
          const info = infos[c.otherUserId]
          if (!info) return c
          const updates: Partial<Conversation> = {}
          if (info.avatar && !c.otherUserAvatar) updates.otherUserAvatar = info.avatar
          // 昵称为空或纯数字时用 API 返回的昵称覆盖
          if (info.nick && (!c.otherUserName || isPureDigits(c.otherUserName))) updates.otherUserName = info.nick
          return Object.keys(updates).length > 0 ? { ...c, ...updates } : c
        }),
      )
    }

    ;(async () => {
      for (let i = 0; i < queries.length; i += BATCH_SIZE) {
        if (cancelled) break
        const batch = queries.slice(i, i + BATCH_SIZE)
        try {
          const infos = await queryUserInfos(activeAccountId, batch)
          if (!cancelled && infos && Object.keys(infos).length > 0) {
            applyInfos(infos)
          }
        } catch {
          // 单批失败不影响后续批次
        }
      }
    })()

    return () => { cancelled = true }
  }, [activeAccountId, missingInfoKey]) // eslint-disable-line react-hooks/exhaustive-deps

  // ==================== 加载聊天记录 ====================
  const repairCachedSummary = useCallback((accountId: string, cid: string, items: ChatMessage[]) => {
    const cached = convsCacheRef.current[accountId]
    if (!cached) return
    cached.convs = repairConversationSummary(cached.convs, cid, items)
    if (accountId === activeAccountIdRef.current) {
      conversationsRef.current = cached.convs
      setConversations(cached.convs)
    }
  }, [])

  const catchUpActiveConversation = useCallback(async (accountId: string, cid: string) => {
    if (accountId !== activeAccountIdRef.current || cid !== activeCidRef.current) return
    const requestId = ++msgRequestIdRef.current
    const generation = recoveryRef.current.generation(accountId)
    const isCurrent = () => requestId === msgRequestIdRef.current &&
      generation === recoveryRef.current.generation(accountId) &&
      accountId === activeAccountIdRef.current && cid === activeCidRef.current
    const cached = msgsCacheRef.current[accountId]?.[cid] ?? {
      msgs: messagesRef.current, hasMore: historyPageRef.current.msgHasMore, cursor: msgCursorRef.current,
    }
    if (cached.source !== CHAT_HISTORY_SOURCE) recoveryRef.current.messages.get(accountId)?.delete(cid)
    const boundary = recoveryRef.current.ensureMessage(accountId, cid, cached.source === CHAT_HISTORY_SOURCE
      ? cached : { msgs: [], hasMore: false, cursor: null, source: CHAT_HISTORY_SOURCE })
    setLoadingMsgs(true)
    await recoverMessages(async (cursor, limit) => {
      const page = await getMessages(accountId, cid, cursor, limit)
      return { items: page.messages, hasMore: page.hasMore, nextCursor: page.nextCursor }
    }, boundary, isCurrent).then((result) => {
      if (!result || !isCurrent()) return
      const current = msgsCacheRef.current[accountId]?.[cid]?.msgs ?? messagesRef.current
      const next: MessageCache = { source: CHAT_HISTORY_SOURCE, contentVersion: CHAT_CONTENT_VERSION, msgs: mergeMessages(result.items, current),
        hasMore: result.hasMore, cursor: result.cursor }
      msgsCacheRef.current[accountId] ??= {}
      msgsCacheRef.current[accountId][cid] = next
      repairCachedSummary(accountId, cid, next.msgs)
      if (result.complete) recoveryRef.current.messages.get(accountId)?.delete(cid)
      else boundary.nextCursor = result.cursor
      messagesRef.current = next.msgs
      msgCursorRef.current = next.cursor
      setMessages(next.msgs)
      setMsgHasMore(next.hasMore)
      setMsgCursor(next.cursor)
      if (!result.complete) {
        addToast({ message: '断线期间消息较多，请点击加载更多继续补齐', type: 'warning' })
      }
    }).catch((error: Error) => {
      if (isCurrent()) addToast({ message: error.message, type: 'error' })
    }).finally(() => {
      if (requestId === msgRequestIdRef.current) setLoadingMsgs(false)
    })
  }, [addToast, repairCachedSummary])

  const loadMessages = useCallback(async (accountId: string, cid: string, append = false) => {
    if (!accountId || !cid) return
    const contentCache = msgsCacheRef.current[accountId]?.[cid]
    if (contentCache && needsMessageContentUpgrade(contentCache)) recoveryRef.current.ensureMessage(accountId, cid, contentCache)
    if (recoveryRef.current.message(accountId, cid)) {
      await catchUpActiveConversation(accountId, cid)
      return
    }
    const requestId = ++msgRequestIdRef.current
    const generation = recoveryRef.current.generation(accountId)
    const isCurrent = () => requestId === msgRequestIdRef.current &&
      generation === recoveryRef.current.generation(accountId) &&
      accountId === activeAccountIdRef.current && cid === activeCidRef.current
    setLoadingMsgs(true)
    const cached = msgsCacheRef.current[accountId]?.[cid]
    await getMessages(accountId, cid, append && cached?.source === CHAT_HISTORY_SOURCE ? msgCursorRef.current ?? undefined : undefined)
      .then((result) => {
        if (!isCurrent()) return
        const current = msgsCacheRef.current[accountId]?.[cid]?.msgs ?? messagesRef.current
        const next = { source: CHAT_HISTORY_SOURCE, contentVersion: CHAT_CONTENT_VERSION, msgs: mergeMessages(result.messages, current),
          hasMore: result.hasMore, cursor: result.nextCursor }
        msgsCacheRef.current[accountId] ??= {}
        msgsCacheRef.current[accountId][cid] = next
        repairCachedSummary(accountId, cid, next.msgs)
        messagesRef.current = next.msgs
        msgCursorRef.current = next.cursor
        setMessages(next.msgs)
        setMsgHasMore(next.hasMore)
        setMsgCursor(next.cursor)
      }).catch((error: Error) => {
        if (isCurrent()) addToast({ message: error.message, type: 'error' })
      }).finally(() => {
        if (requestId === msgRequestIdRef.current) setLoadingMsgs(false)
      })
  }, [addToast, catchUpActiveConversation, repairCachedSummary])

  // 边界已在断线时冻结，等待会话补偿期间收到的实时消息不会推进消息补偿终点。
  const reloadCurrentChat = useCallback(async (accountId: string) => {
    await loadConversations(accountId, false, true)
    if (accountId !== activeAccountIdRef.current) return
    const cid = activeCidRef.current
    if (cid) await catchUpActiveConversation(accountId, cid)
  }, [catchUpActiveConversation, loadConversations])

  useEffect(() => {
    reloadCurrentChatRef.current = reloadCurrentChat
  }, [reloadCurrentChat])

  const handleRefreshHistory = () => {
    if (!activeAccountId || loadingConvs || loadingMsgs) return
    delete blacklistCacheRef.current[`${activeAccountId}:${activeCid}`]
    setHistoryRefreshVersion((version) => version + 1)
    prepareAccountRecovery(activeAccountId)
    void reloadCurrentChat(activeAccountId)
  }

  /** 账号和会话切换共用恢复入口，后台缓存待补偿时立即拉取缺失消息。 */
  const restoreConversation = useCallback((accountId: string, cid: string) => {
    msgRequestIdRef.current += 1
    const cached = msgsCacheRef.current[accountId]?.[cid]
    const restored = cached ?? { msgs: [], hasMore: false, cursor: null }
    repairCachedSummary(accountId, cid, restored.msgs)
    messagesRef.current = restored.msgs
    msgCursorRef.current = restored.cursor
    setMessages(restored.msgs)
    setMsgHasMore(restored.hasMore)
    setMsgCursor(restored.cursor)
    setLoadingMsgs(false)
    if (cid && (!cached || cached.source !== CHAT_HISTORY_SOURCE || needsMessageContentUpgrade(cached) || recoveryRef.current.message(accountId, cid))) {
      void loadMessages(accountId, cid)
    }
  }, [loadMessages, repairCachedSummary])

  // 选中账号时恢复缓存；离线期间产生的缺口通过同一补偿入口补齐。
  useEffect(() => {
    const accountId = activeAccountId
    const account = accountsRef.current.find((item) => item.account_id === accountId)
    if (!historyReady || !accountId || !account) {
      setConversations([])
      selectCid('')
      restoreConversation(accountId, '')
      return
    }
    const cached = convsCacheRef.current[accountId]
    conversationsRef.current = cached?.convs ?? []
    convCursorRef.current = cached?.cursor ?? null
    setConversations(cached?.convs ?? [])
    setConvHasMore(cached?.hasMore ?? false)
    setConvCursor(cached?.cursor ?? null)
    if (account.connected && (!cached || cached.source !== CHAT_HISTORY_SOURCE || recoveryRef.current.conversations.has(accountId))) {
      void loadConversations(accountId, false, !!cached)
    }
    const cid = activeConvPerAccountRef.current[accountId] || ''
    selectCid(cid)
    restoreConversation(accountId, cid)
  }, [activeAccountId, historyReady]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleSelectConversation = (cid: string) => {
    selectCid(cid)
    setMobileTab('chat')
    setConversations((prev) => prev.map((conversation) =>
      conversation.cid === cid ? { ...conversation, unreadCount: 0 } : conversation,
    ))
    restoreConversation(activeAccountId, cid)
  }

  const activeConversation = conversations.find((c) => c.cid === activeCid)

  useEffect(() => {
    let cancelled = false
    setIsOfficiallyBlocked(false)
    if (!activeAccountId || !activeCid) return
    const key = `${activeAccountId}:${activeCid}`
    if (key in blacklistCacheRef.current) {
      setIsOfficiallyBlocked(blacklistCacheRef.current[key])
      return
    }
    getOfficialBlacklistStatus(activeAccountId, activeCid)
      .then((blocked) => {
        blacklistCacheRef.current[key] = blocked
        schedulePersist()
        if (!cancelled) setIsOfficiallyBlocked(blocked)
      })
      .catch(() => { if (!cancelled) setIsOfficiallyBlocked(false) })
    return () => { cancelled = true }
  }, [activeAccountId, activeCid, historyRefreshVersion, schedulePersist])

  const loadCustomerOrders = useCallback(async (silent = false) => {
    if (!activeAccountId || !activeConversation?.otherUserId) {
      setCustomerOrders([])
      return
    }
    if (!silent) setLoadingOrders(true)
    try {
      const data = await getCustomerOrders(activeAccountId, activeConversation.otherUserId, activeCid)
      setCustomerOrders(data)
    } catch (e: any) {
      addToast({ message: e.message || '获取客户订单失败', type: 'error' })
    } finally {
      if (!silent) setLoadingOrders(false)
    }
  }, [activeAccountId, activeCid, activeConversation?.otherUserId, addToast])

  useEffect(() => {
    loadCustomerOrders()
  }, [loadCustomerOrders])

  useEffect(() => {
    reloadOrdersRef.current = () => { void loadCustomerOrders(true) }
  }, [loadCustomerOrders])

  useEffect(() => {
    if (!activeCid) return
    const timer = window.setInterval(() => { void loadCustomerOrders(true) }, 15000)
    return () => window.clearInterval(timer)
  }, [activeCid, loadCustomerOrders])

  const loadQuickPhrases = useCallback(async () => {
    try {
      setQuickPhrases(await getQuickPhrases())
    } catch (e: any) {
      addToast({ message: e.message || '获取快捷短语失败', type: 'error' })
    }
  }, [addToast])

  useEffect(() => {
    loadQuickPhrases()
  }, [loadQuickPhrases])

  const resetPhraseForm = () => {
    setEditingPhraseId(null)
    setPhraseTitle('')
    setPhraseContent('')
  }

  const handleSavePhrase = async () => {
    if (!phraseTitle.trim() || !phraseContent.trim() || savingPhrase) return
    setSavingPhrase(true)
    try {
      const payload = { title: phraseTitle.trim(), content: phraseContent.trim(), sort_order: 0 }
      if (editingPhraseId) {
        await updateQuickPhrase(editingPhraseId, payload)
      } else {
        await createQuickPhrase(payload)
      }
      resetPhraseForm()
      await loadQuickPhrases()
      addToast({ message: editingPhraseId ? '快捷短语已更新' : '快捷短语已添加', type: 'success' })
    } catch (e: any) {
      addToast({ message: e.message || '保存快捷短语失败', type: 'error' })
    } finally {
      setSavingPhrase(false)
    }
  }

  const handleDeletePhrase = async (id: number) => {
    if (!(await requestConfirm({ message: '确认删除这条快捷短语吗？', confirmText: '删除', type: 'danger' }))) return
    try {
      await deleteQuickPhrase(id)
      if (editingPhraseId === id) resetPhraseForm()
      await loadQuickPhrases()
    } catch (e: any) {
      addToast({ message: e.message || '删除快捷短语失败', type: 'error' })
    }
  }

  const handleSyncOrders = async () => {
    if (!activeAccountId || loadingOrders) return
    setLoadingOrders(true)
    try {
      const res = await fetchXianyuOrders(activeAccountId)
      addToast({ message: res.message || '订单同步完成', type: res.success ? 'success' : 'error' })
      await loadCustomerOrders()
    } catch (e: any) {
      addToast({ message: e.message || '同步订单失败', type: 'error' })
    } finally {
      setLoadingOrders(false)
    }
  }

  const handleDeliverOrder = async (orderNo: string) => {
    if (!(await requestConfirm({ message: `确认立即发货订单 ${orderNo} 吗？`, confirmText: '发货' }))) return
    setDeliveringOrderNo(orderNo)
    try {
      const res = await manualDelivery(orderNo)
      addToast({ message: res.message || (res.success ? '发货成功' : '发货失败'), type: res.success ? 'success' : 'error' })
      await loadCustomerOrders()
    } catch (e: any) {
      addToast({ message: e.message || '发货失败', type: 'error' })
    } finally {
      setDeliveringOrderNo('')
    }
  }


  // 消息变化时自动滚动到底部
  const handleNoLogisticsDelivery = async (orderNo: string) => {
    if (!(await requestConfirm({ message: `确认将订单 ${orderNo} 标记为无物流发货吗？`, confirmText: '无物流发货' }))) return
    setConfirmingOrderNo(orderNo)
    try {
      const res = await noLogisticsDelivery(orderNo)
      addToast({ message: res.message || (res.success ? '无物流发货成功' : '无物流发货失败'), type: res.success ? 'success' : 'error' })
      await loadCustomerOrders(true)
    } catch (e: any) {
      addToast({ message: e.message || '无物流发货失败', type: 'error' })
    } finally {
      setConfirmingOrderNo('')
    }
  }

  const handleCancelOrder = async (orderNo: string) => {
    if (!(await requestConfirm({ message: `确认取消客户订单 ${orderNo} 吗？取消后无法恢复。`, confirmText: '取消订单', type: 'danger' }))) return
    setCancellingOrderNo(orderNo)
    try {
      const res = await cancelOrder(orderNo)
      addToast({ message: res.message || (res.success ? '订单已取消' : '取消订单失败'), type: res.success ? 'success' : 'error' })
      await loadCustomerOrders(true)
    } catch (e: any) {
      addToast({ message: e.message || '取消订单失败', type: 'error' })
    } finally {
      setCancellingOrderNo('')
    }
  }

  const handleViewOrderDetail = async (orderNo: string) => {
    setLoadingOrderDetail(true)
    try {
      const res = await getOrderDetail(orderNo, true)
      setOrderDetail(res.data)
      await loadCustomerOrders(true)
    } catch (e: any) {
      addToast({ message: e.message || '获取订单详情失败', type: 'error' })
    } finally {
      setLoadingOrderDetail(false)
    }
  }

  const handleBlacklistCustomer = async () => {
    if (!activeConversation || !activeAccountId || blacklisting) return
    const action = isOfficiallyBlocked ? 'remove' : 'add'
    const label = isOfficiallyBlocked ? '解除黑名单' : '加入黑名单'
    if (!(await requestConfirm({
      title: `确认${label}`,
      message: `确认在闲鱼官方${label}客户 ${activeConversation.otherUserName || activeConversation.otherUserId} 吗？`,
      confirmText: label,
      type: isOfficiallyBlocked ? 'warning' : 'danger',
    }))) return
    setBlacklisting(true)
    try {
      const res = await changeOfficialBlacklist(activeAccountId, activeConversation.cid, action)
      if (!res.success) throw new Error(res.message || '黑名单操作失败')
      setIsOfficiallyBlocked(action === 'add')
      blacklistCacheRef.current[`${activeAccountId}:${activeConversation.cid}`] = action === 'add'
      schedulePersist()
      addToast({ message: res.message || `${label}成功`, type: 'success' })
    } catch (e: any) {
      addToast({ message: e.message || '加入黑名单失败', type: 'error' })
    } finally {
      setBlacklisting(false)
    }
  }

  const handleRecallMessage = async (msg: ChatMessage) => {
    if (!activeAccountId || !msg.messageId || recallingMessageId) return
    if (!canRecallMessage(msg)) {
      addToast({ message: '消息发送超过两分钟，无法撤回', type: 'error' })
      return
    }
    if (!(await requestConfirm({
      title: '撤回消息',
      message: '确认撤回这条消息吗？撤回仅支持发送后两分钟内操作。',
      confirmText: '撤回',
      type: 'danger',
    }))) return
    setRecallingMessageId(msg.messageId)
    try {
      const res = await recallMessage(activeAccountId, msg.messageId, msg.time)
      if (!res.success) throw new Error(res.message || '撤回失败')
      setMessages((prev) => prev.map((item) => item.messageId === msg.messageId
        ? { ...item, type: 'system', text: '你撤回了一条消息', images: [] }
        : item))
      addToast({ message: res.message || '消息已撤回', type: 'success' })
    } catch (e: any) {
      addToast({ message: e.message || '撤回失败', type: 'error' })
    } finally {
      setRecallingMessageId('')
    }
  }

  const prevMsgCountRef = useRef(0)
  useEffect(() => {
    if (!msgContainerRef.current) return
    const container = msgContainerRef.current
    // 只有新增消息时才滚动（而不是加载历史）
    if (messages.length > prevMsgCountRef.current || prevMsgCountRef.current === 0) {
      container.scrollTop = container.scrollHeight
    }
    prevMsgCountRef.current = messages.length
  }, [messages])

  // ==================== 发送消息 ====================
  const sendMessageText = async (rawText: string, clearInput = false) => {
    if (!rawText.trim() || !activeAccountId || !activeCid || sending) return

    // 获取当前会话的对方用户ID
    const conv = conversations.find((c) => c.cid === activeCid)
    if (!conv) {
      addToast({ message: '未找到当前会话信息', type: 'error' })
      return
    }

    const text = rawText.trim()
    setSending(true)
    try {
      const res = await sendTextMessage(activeAccountId, activeCid, conv.otherUserId, text)
      // 无论成功失败，都把这条消息展示在聊天记录中；
      // 失败时标记 failed + failReason，气泡前显示红色感叹号，点击查看原因
      const sentMsg: ChatMessage = {
        messageId: res.data?.messageId || '',
        senderId: activeAccountId,
        senderName: '',
        isSelf: true,
        type: 'text',
        text,
        images: [],
        time: Date.now(),
        failed: !res.success,
        failReason: res.success ? undefined : (res.message || '发送失败'),
      }
      if (clearInput) setInputText('')
      setMessages((prev) => appendMsg(prev, sentMsg))
      if (res.success) {
        // 成功才更新会话列表摘要
        setConversations((prev) =>
          prev.map((c) =>
            c.cid === activeCid
              ? { ...c, lastMessageSummary: text.slice(0, 50), lastMessageTime: sentMsg.time }
              : c,
          ),
        )
      } else {
        addToast({ message: res.message || '发送失败', type: 'error' })
      }
    } catch (e: any) {
      // 网络等异常：同样以失败态展示该条消息
      const failReason = e?.message || '发送失败'
      const sentMsg: ChatMessage = {
        messageId: '',
        senderId: activeAccountId,
        senderName: '',
        isSelf: true,
        type: 'text',
        text,
        images: [],
        time: Date.now(),
        failed: true,
        failReason,
      }
      if (clearInput) setInputText('')
      setMessages((prev) => appendMsg(prev, sentMsg))
      addToast({ message: failReason, type: 'error' })
    } finally {
      setSending(false)
    }
  }

  // ==================== 发送图片 ====================
  const clearPendingImage = useCallback(() => {
    setPendingImage((prev) => {
      if (prev) URL.revokeObjectURL(prev.previewUrl)
      return null
    })
  }, [])

  useEffect(() => {
    pendingImageRef.current = pendingImage
  }, [pendingImage])

  useEffect(() => () => {
    if (pendingImageRef.current) URL.revokeObjectURL(pendingImageRef.current.previewUrl)
  }, [])

  useEffect(() => {
    clearPendingImage()
  }, [activeAccountId, activeCid, clearPendingImage])

  const getClipboardImageFileName = (type: string) => {
    const extMap: Record<string, string> = {
      'image/jpeg': 'jpg',
      'image/png': 'png',
      'image/webp': 'webp',
      'image/gif': 'gif',
    }
    return `clipboard-${Date.now()}.${extMap[type] || 'png'}`
  }

  const handlePickImage = () => {
    if (sending) return
    imageInputRef.current?.click()
  }

  const validateImageFile = (file: File) => {
    if (!file.type.startsWith('image/')) {
      addToast({ message: '请选择图片文件', type: 'error' })
      return false
    }
    if (file.size > 10 * 1024 * 1024) {
      addToast({ message: '图片大小不能超过10MB', type: 'error' })
      return false
    }
    return true
  }

  const sendImageFile = async (file: File) => {
    if (!file || !activeAccountId || !activeCid || sending) return false

    if (!validateImageFile(file)) return false

    const conv = conversations.find((c) => c.cid === activeCid)
    if (!conv) {
      addToast({ message: '未找到当前会话信息', type: 'error' })
      return false
    }

    setSending(true)
    try {
      const res = await sendImageMessage(activeAccountId, activeCid, conv.otherUserId, file)
      // 成功用CDN地址；失败则用本地预览地址，保证用户都能看到所发图片
      const displayUrl = res.success && res.data?.imageUrl ? res.data.imageUrl : URL.createObjectURL(file)
      // 无论成功失败，都把这条图片消息展示在聊天记录中
      const sentMsg: ChatMessage = {
        messageId: res.data?.messageId || '',
        senderId: activeAccountId,
        senderName: '',
        isSelf: true,
        type: 'image',
        text: '',
        images: [displayUrl],
        time: Date.now(),
        failed: !res.success,
        failReason: res.success ? undefined : (res.message || '发送失败'),
      }
      setMessages((prev) => appendMsg(prev, sentMsg))
      if (res.success) {
        setConversations((prev) =>
          prev.map((c) =>
            c.cid === activeCid
              ? { ...c, lastMessageSummary: '[图片]', lastMessageTime: sentMsg.time }
              : c,
          ),
        )
      } else {
        addToast({ message: res.message || '发送失败', type: 'error' })
      }
      return res.success
    } catch (e: any) {
      const failReason = e?.message || '发送失败'
      const displayUrl = URL.createObjectURL(file)
      const sentMsg: ChatMessage = {
        messageId: '',
        senderId: activeAccountId,
        senderName: '',
        isSelf: true,
        type: 'image',
        text: '',
        images: [displayUrl],
        time: Date.now(),
        failed: true,
        failReason,
      }
      setMessages((prev) => appendMsg(prev, sentMsg))
      addToast({ message: failReason, type: 'error' })
      return false
    } finally {
      setSending(false)
    }
  }

  const handleImageSelected = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    // 选完即清空 value，保证同一张图片可重复选择触发 onChange
    e.target.value = ''
    if (!file) return
    await sendImageFile(file)
  }

  const handlePasteImage = async (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const items = Array.from(e.clipboardData?.items || [])
    const imageItem = items.find((item) => item.kind === 'file' && item.type.startsWith('image/'))
    if (!imageItem) return

    e.preventDefault()
    const blob = imageItem.getAsFile()
    if (!blob) {
      addToast({ message: '读取剪贴板图片失败', type: 'error' })
      return
    }

    const file = new File([blob], getClipboardImageFileName(blob.type), {
      type: blob.type || 'image/png',
      lastModified: Date.now(),
    })
    if (!validateImageFile(file)) return

    setPendingImage((prev) => {
      if (prev) URL.revokeObjectURL(prev.previewUrl)
      return { file, previewUrl: URL.createObjectURL(file) }
    })
  }

  const handleSendMessage = async () => {
    if (pendingImage) {
      const file = pendingImage.file
      clearPendingImage()
      await sendImageFile(file)
      return
    }
    sendMessageText(inputText, true)
  }

  // ==================== 时间格式化 ====================
  const formatTime = formatChatTime

  // ==================== 渲染 ====================
  // 手机端各 Tab 对应未读 / 状态提示
  const totalUnread = conversations.reduce((sum, c) => sum + (c.unreadCount || 0), 0)
  const tabItems: Array<{ key: MobileTab; label: string; badge?: number; disabled?: boolean }> = [
    { key: 'accounts', label: '账号' },
    { key: 'convs', label: '会话', badge: totalUnread, disabled: !activeAccountId },
    { key: 'chat', label: '聊天', disabled: !activeCid },
    { key: 'tools', label: '工作区', disabled: !activeCid },
  ]

  return (
    <div className="flex flex-col h-[calc(100vh-120px)]">
      {/* 手机端顶部 Tab 切换栏（桌面端隐藏） */}
      <div className="md:hidden mb-2 flex bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
        {tabItems.map((item) => {
          const active = mobileTab === item.key
          return (
            <button
              key={item.key}
              type="button"
              disabled={item.disabled}
              onClick={() => setMobileTab(item.key)}
              className={`flex-1 py-2 text-xs font-medium transition-colors relative ${
                active
                  ? 'bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-300'
                  : 'text-gray-600 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700/40'
              } disabled:opacity-40 disabled:cursor-not-allowed`}
            >
              {item.label}
              {item.badge && item.badge > 0 ? (
                <span className="absolute top-1 right-1/2 translate-x-6 bg-red-500 text-white text-[10px] rounded-full px-1 min-w-[16px] text-center">
                  {item.badge > 99 ? '99+' : item.badge}
                </span>
              ) : null}
            </button>
          )
        })}
      </div>

      <div className="flex flex-1 min-h-0 flex-col md:flex-row gap-3">
      {/* 左侧：账号列表 */}
      <div className={`${mobileTab === 'accounts' ? 'flex' : 'hidden'} md:flex w-full md:w-56 flex-shrink-0 bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 flex-col min-h-0`}>
        <div className="p-3 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
          <span className="font-medium text-sm text-gray-700 dark:text-gray-300">账号列表</span>
          <button
            onClick={() => loadAccounts()}
            className="p-1 hover:bg-gray-100 dark:hover:bg-gray-700 rounded"
            title="刷新"
          >
            <RefreshCw className={`w-4 h-4 text-gray-500 ${loadingAccounts ? 'animate-spin' : ''}`} />
          </button>
        </div>
        <div
          ref={accountListRef}
          className="flex-1 overflow-y-auto p-2 space-y-1"
          onScroll={(e) => {
            const el = e.currentTarget
            if (el.scrollHeight - el.scrollTop - el.clientHeight < 40) {
              loadMoreAccounts()
            }
          }}
        >
          {loadingAccounts && accounts.length === 0 ? (
            <div className="flex justify-center py-8">
              <Loader2 className="w-5 h-5 animate-spin text-blue-500" />
            </div>
          ) : accounts.length === 0 ? (
            <p className="text-center text-sm text-gray-400 py-8">暂无可用账号</p>
          ) : (
            <>
              {accounts.map((acc) => (
                <div
                  key={acc.account_id}
                  className={`p-2 rounded-lg transition-colors text-sm cursor-pointer ${
                    activeAccountId === acc.account_id
                      ? 'bg-blue-50 dark:bg-blue-900/30 border border-blue-200 dark:border-blue-700'
                      : 'hover:bg-gray-50 dark:hover:bg-gray-700/50 border border-transparent'
                  }`}
                  onClick={() => handleSelectAccount(acc)}
                  title={`${acc.display_name || acc.remark || acc.account_id}\n(${acc.account_id})`}
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2 min-w-0 flex-1">
                      <User className="w-4 h-4 flex-shrink-0 text-gray-400" />
                      <div className="min-w-0 flex-1">
                        <span className="block truncate text-gray-700 dark:text-gray-300">
                          {acc.display_name || acc.remark || acc.account_id}
                        </span>
                        {(acc.display_name || acc.remark) && (
                          <span className="block truncate text-xs text-gray-400 dark:text-gray-500">
                            {acc.remark && acc.remark !== acc.display_name ? acc.remark : acc.account_id}
                          </span>
                        )}
                        {acc.owner && (
                          <span className="block truncate text-xs text-blue-400 dark:text-blue-500">
                            {acc.owner}
                          </span>
                        )}
                      </div>
                    </div>
                    {acc.connected ? (
                      <button
                        onClick={(e) => { e.stopPropagation(); handleDisconnect(acc.account_id) }}
                        className="p-1 text-red-400 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-900/30 rounded"
                        title="断开"
                      >
                        <LogOut className="w-3.5 h-3.5" />
                      </button>
                    ) : (
                      <button
                        onClick={(e) => { e.stopPropagation(); handleConnect(acc.account_id) }}
                        disabled={!!connectingId}
                        className="p-1 text-green-500 hover:text-green-700 hover:bg-green-50 dark:hover:bg-green-900/30 rounded disabled:opacity-50"
                        title="连接"
                      >
                        {connectingId === acc.account_id ? (
                          <Loader2 className="w-3.5 h-3.5 animate-spin" />
                        ) : (
                          <LogIn className="w-3.5 h-3.5" />
                        )}
                      </button>
                    )}
                  </div>
                  <div className="mt-1 flex items-center gap-1">
                    <span
                      className={`inline-block w-1.5 h-1.5 rounded-full ${
                        acc.connected ? 'bg-green-500' : acc.status !== 'active' ? 'bg-orange-400' : 'bg-gray-300'
                      }`}
                    />
                    <span className="text-xs text-gray-400">
                      {acc.connected ? '已连接' : acc.status !== 'active' ? '已禁用' : '未连接'}
                    </span>
                  </div>
                </div>
              ))}
              {accountHasMore && (
                <button
                  onClick={loadMoreAccounts}
                  disabled={loadingAccounts}
                  className="w-full py-2 text-xs text-blue-500 hover:bg-gray-50 dark:hover:bg-gray-700/30 disabled:opacity-50"
                >
                  {loadingAccounts ? '加载中...' : '加载更多'}
                </button>
              )}
            </>
          )}
        </div>
      </div>

      {/* 中间：会话列表 */}
      <div className={`${mobileTab === 'convs' ? 'flex' : 'hidden'} md:flex w-full md:w-72 flex-shrink-0 bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 flex-col min-h-0`}>
        <div className="p-3 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
          <span className="font-medium text-sm text-gray-700 dark:text-gray-300">会话列表</span>
          {activeAccountId && (
            <button
              onClick={handleRefreshHistory}
              disabled={loadingConvs || loadingMsgs}
              className="p-1 hover:bg-gray-100 dark:hover:bg-gray-700 rounded disabled:opacity-50"
              title="从闲鱼同步最新会话和当前聊天记录"
            >
              <RefreshCw className={`w-4 h-4 text-gray-500 ${loadingConvs ? 'animate-spin' : ''}`} />
            </button>
          )}
        </div>
        {activeAccountId && (
          <p className="px-3 py-2 text-xs leading-5 text-gray-400 border-b border-gray-100 dark:border-gray-700/50">
            历史记录保存在本机，点击刷新同步最新记录。
          </p>
        )}
        <div
          className="flex-1 overflow-y-auto"
          onScroll={(e) => {
            const el = e.currentTarget
            if (el.scrollHeight - el.scrollTop - el.clientHeight < 40 && convHasMore && !loadingConvs && activeAccountId) {
              loadConversations(activeAccountId, true)
            }
          }}
        >
          {!activeAccountId ? (
            <p className="text-center text-sm text-gray-400 py-12">请先选择账号</p>
          ) : loadingConvs && conversations.length === 0 ? (
            <div className="flex justify-center py-12">
              <Loader2 className="w-5 h-5 animate-spin text-blue-500" />
            </div>
          ) : conversations.length === 0 ? (
            <div className="text-center text-sm text-gray-400 py-12">
              {accounts.find((a) => a.account_id === activeAccountId)?.connected
                ? convHasMore ? '当前页暂无可显示会话' : '暂无会话'
                : '请先连接账号'}
              {convHasMore && (
                <button onClick={() => loadConversations(activeAccountId, true)} disabled={loadingConvs}
                  className="block mx-auto mt-3 text-xs text-blue-500 disabled:opacity-50">
                  {loadingConvs ? '加载中...' : '继续加载会话'}
                </button>
              )}
            </div>
          ) : (
            <>
              {conversations.map((conv, idx) => (
                <div
                  key={conv.cid || `conv-${idx}`}
                  className={`px-3 py-2.5 cursor-pointer border-b border-gray-100 dark:border-gray-700/50 transition-colors ${
                    activeCid === conv.cid
                      ? 'bg-blue-50 dark:bg-blue-900/20'
                      : 'hover:bg-gray-50 dark:hover:bg-gray-700/30'
                  }`}
                  onClick={() => handleSelectConversation(conv.cid)}
                >
                  <div className="flex items-center gap-2">
                    {conv.otherUserAvatar ? (
                      <img
                        src={conv.otherUserAvatar}
                        className="w-9 h-9 rounded-full flex-shrink-0 object-cover"
                        alt=""
                      />
                    ) : (
                      <div className="w-9 h-9 rounded-full bg-gray-200 dark:bg-gray-600 flex items-center justify-center flex-shrink-0">
                        <User className="w-5 h-5 text-gray-400" />
                      </div>
                    )}
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center justify-between">
                        <span className="text-sm font-medium text-gray-800 dark:text-gray-200 truncate">
                          {conv.otherUserName || conv.otherUserId || '未知用户'}
                        </span>
                        <span className="text-xs text-gray-400 flex-shrink-0 ml-2">
                          {formatTime(conv.lastMessageTime)}
                        </span>
                      </div>
                      {conv.itemTitle && (
                        <div className="text-xs text-blue-400 truncate mt-0.5">
                          {conv.itemTitle}
                        </div>
                      )}
                      <div className="flex items-center justify-between mt-0.5">
                        <span className="text-xs text-gray-400 truncate">
                          {conv.lastMessageSummary || '暂无消息'}
                        </span>
                        {conv.unreadCount > 0 && (
                          <span className="ml-2 flex-shrink-0 bg-red-500 text-white text-xs rounded-full px-1.5 min-w-[18px] text-center">
                            {conv.unreadCount > 99 ? '99+' : conv.unreadCount}
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                </div>
              ))}
              {convHasMore && (
                <button
                  onClick={() => loadConversations(activeAccountId, true)}
                  disabled={loadingConvs}
                  className="w-full py-2 text-xs text-blue-500 hover:bg-gray-50 dark:hover:bg-gray-700/30 disabled:opacity-50"
                >
                  {loadingConvs ? '加载中...' : '加载更多'}
                </button>
              )}
            </>
          )}
        </div>
      </div>

      {/* 右侧：聊天记录 */}
      <div className={`${mobileTab === 'chat' ? 'flex' : 'hidden'} md:flex flex-1 min-w-0 min-h-0 bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 flex-col`}>
        {/* 聊天头部 */}
        <div className="p-3 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
          <MessageCircle className="w-4 h-4 text-gray-500" />
          <span className="font-medium text-sm text-gray-700 dark:text-gray-300">
            {activeCid
              ? conversations.find((c) => c.cid === activeCid)?.otherUserName || '聊天记录'
              : '聊天记录'}
          </span>
          </div>
          {activeCid && (
            <button onClick={handleBlacklistCustomer} disabled={blacklisting} className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded border border-red-200 text-red-500 hover:bg-red-50 disabled:opacity-40" title={isOfficiallyBlocked ? '解除闲鱼官方黑名单' : '加入闲鱼官方黑名单'}>
              {blacklisting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Ban className="w-3.5 h-3.5" />}
              {isOfficiallyBlocked ? '解除黑名单' : '加入黑名单'}
            </button>
          )}
        </div>
        {/* 消息区域 */}
        <div ref={msgContainerRef} className="flex-1 overflow-y-auto bg-slate-50/70 dark:bg-gray-900/30 p-4 space-y-4">
          {!activeCid ? (
            <p className="text-center text-sm text-gray-400 py-12">请选择一个会话查看聊天记录</p>
          ) : loadingMsgs && messages.length === 0 ? (
            <div className="flex justify-center py-12">
              <Loader2 className="w-5 h-5 animate-spin text-blue-500" />
            </div>
          ) : (
            <>
              {/* 加载更多历史 */}
              {msgHasMore && (
                <div className="text-center">
                  <button
                    onClick={() => loadMessages(activeAccountId, activeCid, true)}
                    disabled={loadingMsgs}
                    className="inline-flex items-center gap-1 text-xs text-blue-500 hover:underline disabled:opacity-50"
                  >
                    <ChevronUp className="w-3 h-3" />
                    {loadingMsgs ? '加载中...' : '加载更早的消息'}
                  </button>
                </div>
              )}
              {messages.map((msg, idx) => (
                <div
                  key={msg.messageId || idx}
                  className={`flex w-full items-start gap-2.5 py-1 ${msg.isSelf ? 'flex-row-reverse' : ''}`}
                >
                  <div className="h-8 w-8 shrink-0 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-600 flex items-center justify-center">
                    {msg.senderAvatar || (!msg.isSelf && activeConversation?.otherUserAvatar) ? (
                      <img
                        src={msg.senderAvatar || activeConversation?.otherUserAvatar}
                        alt={msg.isSelf ? '我' : '对方头像'}
                        className="h-full w-full object-cover"
                      />
                    ) : (
                      <User className="h-4 w-4 text-gray-400" />
                    )}
                  </div>
                  <div className="min-w-0 max-w-[75%] sm:max-w-[70%]">
                    {/* 发送者名称 */}
                    <div
                      className={`text-xs text-gray-400 mb-1 ${
                        msg.isSelf ? 'text-right' : 'text-left'
                      }`}
                    >
                      {msg.isSelf ? '我' : activeConversation?.otherUserName || msg.senderName || '对方'}
                      <span className="ml-2">{formatTime(msg.time)}</span>
                    </div>
                    {/* 消息气泡（失败的本地消息在气泡前显示红色感叹号，点击查看原因） */}
                    <div className={`flex items-center gap-1.5 ${msg.isSelf ? 'flex-row-reverse' : ''}`}>
                      <div
                        className={`${msg.type === 'card' || msg.card ? 'rounded-xl' : 'rounded-lg px-3 py-2'} text-sm break-words ${
                          msg.type === 'card' || msg.card ? '' :
                          msg.isSelf
                            ? 'bg-blue-500 text-white'
                            : 'bg-gray-100 dark:bg-gray-700 text-gray-800 dark:text-gray-200'
                        } ${msg.type === 'system' ? '!bg-gray-200 dark:!bg-gray-600 text-center text-gray-500 dark:text-gray-400 text-xs' : ''}`}
                      >
                        {msg.card ? <ChatMessageCard card={msg.card} isSelf={msg.isSelf} /> : isLegacyUnsupportedMessage(msg) ? (
                          <ChatMessageCard card={{kind: 'unknown', title: '闲鱼消息', text: loadingMsgs ? '正在补齐消息内容…' : '这条历史消息的详情暂时不可用，可在闲鱼中查看。', fallback: true}} isSelf={msg.isSelf} />
                        ) : msg.type === 'image' && msg.images.length > 0 ? (
                          <div className="space-y-1">
                            {msg.images.map((url, i) => (
                              <img
                                key={i}
                                src={url}
                                className="max-w-full rounded max-h-48 object-contain cursor-pointer hover:opacity-80 transition-opacity"
                                alt="图片消息"
                                onClick={() => setPreviewImage(url)}
                              />
                            ))}
                          </div>
                        ) : (
                          <span className="whitespace-pre-wrap">{msg.text}</span>
                        )}
                      </div>
                      {msg.isSelf && msg.failed && (
                        <button
                          type="button"
                          title="发送失败，点击查看原因"
                          onClick={() =>
                            addToast({ message: msg.failReason || '发送失败', type: 'error' })
                          }
                          className="flex-shrink-0 text-red-500 hover:text-red-600 transition-colors"
                        >
                          <AlertCircle className="w-4 h-4" />
                        </button>
                      )}
                    </div>
                    {canRecallMessage(msg) && (
                      <div className="mt-1 text-right">
                        <button
                          onClick={() => handleRecallMessage(msg)}
                          disabled={!!recallingMessageId}
                          className="text-xs text-gray-400 hover:text-red-500 disabled:opacity-40"
                        >
                          {recallingMessageId === msg.messageId ? '撤回中...' : '撤回'}
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              ))}
            </>
          )}
        </div>
        {/* 底部输入框 */}
        {activeCid && (
          <div className="p-3 border-t border-gray-200 dark:border-gray-700">
            <input
              ref={imageInputRef}
              type="file"
              accept="image/*"
              className="hidden"
              onChange={handleImageSelected}
            />
            {pendingImage && (
              <div className="mb-2 inline-flex items-start gap-2 rounded-lg border border-gray-200 dark:border-gray-600 bg-gray-50 dark:bg-gray-700/50 p-2">
                <img
                  src={pendingImage.previewUrl}
                  className="h-20 w-20 rounded object-contain bg-white dark:bg-gray-800 cursor-pointer"
                  alt="待发送图片"
                  onClick={() => setPreviewImage(pendingImage.previewUrl)}
                />
                <div className="min-w-0 max-w-44">
                  <div className="truncate text-xs text-gray-600 dark:text-gray-300">{pendingImage.file.name}</div>
                  <div className="mt-1 text-xs text-gray-400">点击发送按钮后发送图片</div>
                </div>
                <button
                  type="button"
                  onClick={clearPendingImage}
                  disabled={sending}
                  title="移除待发送图片"
                  className="rounded p-1 text-gray-400 hover:bg-gray-200 hover:text-gray-600 dark:hover:bg-gray-600 dark:hover:text-gray-200 disabled:opacity-50"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
            )}
            <div className="flex items-end gap-2">
              <button
                type="button"
                onClick={handlePickImage}
                disabled={sending}
                title="发送图片"
                className="flex-shrink-0 flex items-center justify-center w-9 h-9 text-gray-500 dark:text-gray-300 border border-gray-300 dark:border-gray-600 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                <ImagePlus className="w-4 h-4" />
              </button>
              <textarea
                value={inputText}
                onChange={(e) => setInputText(e.target.value)}
                onPaste={handlePasteImage}
                onKeyDown={(e) => {
                  // Enter 发送；Shift+Enter / Ctrl+Enter 在输入框内换行
                  // 中文输入法选词阶段的回车（compositionend 前）不触发发送
                  const isComposing = e.nativeEvent.isComposing || (e as any).keyCode === 229
                  if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !isComposing) {
                    e.preventDefault()
                    handleSendMessage()
                  }
                }}
                placeholder="输入消息...（Shift+Enter 换行，Enter 发送）"
                disabled={sending}
                rows={1}
                className="flex-1 px-3 py-2 text-sm leading-5 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-800 dark:text-gray-200 placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 resize-none max-h-32 overflow-y-auto whitespace-pre-wrap"
              />
              <button
                onClick={handleSendMessage}
                disabled={sending || (!inputText.trim() && !pendingImage)}
                className="flex-shrink-0 flex items-center gap-1 px-4 h-9 text-sm font-medium text-white bg-blue-500 rounded-lg hover:bg-blue-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {sending ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  <Send className="w-4 h-4" />
                )}
                发送
              </button>
            </div>
          </div>
        )}
      </div>
      {/* 图片预览弹窗 */}
      {/* 右侧工作区：客户订单 + 快捷短语 */}
      <div className={`${mobileTab === 'tools' ? 'flex' : 'hidden'} md:flex w-full md:w-80 flex-shrink-0 min-h-0 flex-col gap-3 overflow-y-auto md:overflow-visible`}>
        <CustomerOrdersPanel
          activeCid={activeCid}
          orders={customerOrders}
          loading={loadingOrders}
          deliveringOrderNo={deliveringOrderNo}
          confirmingOrderNo={confirmingOrderNo}
          cancellingOrderNo={cancellingOrderNo}
          loadingOrderDetail={loadingOrderDetail}
          onSync={handleSyncOrders}
          onViewDetail={handleViewOrderDetail}
          onCancel={handleCancelOrder}
          onNoLogistics={handleNoLogisticsDelivery}
          onDeliver={handleDeliverOrder}
        />
        <QuickPhrasesPanel
          phrases={quickPhrases}
          activeCid={activeCid}
          sending={sending}
          editingPhraseId={editingPhraseId}
          phraseTitle={phraseTitle}
          phraseContent={phraseContent}
          savingPhrase={savingPhrase}
          onSend={(content) => sendMessageText(content)}
          onEdit={(phrase) => { setEditingPhraseId(phrase.id); setPhraseTitle(phrase.title); setPhraseContent(phrase.content) }}
          onDelete={handleDeletePhrase}
          onReset={resetPhraseForm}
          onTitleChange={setPhraseTitle}
          onContentChange={setPhraseContent}
          onSave={handleSavePhrase}
        />
      </div>
      </div>
      <OrderDetailModal
        order={orderDetail}
        fallbackBuyerNick={activeConversation?.otherUserName}
        onClose={() => setOrderDetail(null)}
      />
      {previewImage && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/70"
          onClick={() => setPreviewImage('')}
        >
          <button
            className="absolute top-4 right-4 text-white hover:text-gray-300 transition-colors"
            onClick={() => setPreviewImage('')}
          >
            <X className="w-8 h-8" />
          </button>
          <img
            src={previewImage}
            className="max-w-[90vw] max-h-[90vh] object-contain rounded-lg"
            alt="预览"
            onClick={(e) => e.stopPropagation()}
          />
        </div>
      )}
      <ConfirmModal
        isOpen={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message || ''}
        confirmText={confirmDialog?.confirmText}
        type={confirmDialog?.type}
        onConfirm={() => closeConfirm(true)}
        onCancel={() => closeConfirm(false)}
      />
    </div>
  )
}

export default ChatNew
