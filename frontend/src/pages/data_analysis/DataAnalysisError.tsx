import { AlertCircle, ExternalLink } from 'lucide-react'

export function DataAnalysisError({ message }: { message: string }) {
  const permissionRequired = message.includes('FAIL_BIZ_IDLE_USER_UNAUTHORIZED')

  return (
    <div role="alert" className="rounded-lg border border-amber-200 bg-amber-50 p-4 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200">
      <div className="flex items-start gap-2">
        <AlertCircle className="mt-0.5 h-5 w-5 flex-shrink-0" />
        <div className="space-y-2 text-sm">
          <p className="font-medium">
            {permissionRequired ? '当前闲鱼账号没有数据罗盘访问权限' : '数据查询失败'}
          </p>
          <p>
            {permissionRequired
              ? '账号登录有效不代表已开通卖家工作台或数据罗盘。请使用同一闲鱼账号登录官方卖家工作台，确认是否有相应功能权限；子账号请联系主账号确认授权。'
              : message}
          </p>
          {permissionRequired && (
            <>
              <p>若官方工作台可以正常查看数据，请更新本系统中的闲鱼账号 Cookie 后重试；若官方也提示无权限，需先在闲鱼官方处理开通或授权。</p>
              <a
                href="https://seller.goofish.com/?site=COMMONPRO"
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 font-medium underline underline-offset-2"
              >
                打开闲鱼官方卖家工作台
                <ExternalLink className="h-3.5 w-3.5" />
              </a>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
