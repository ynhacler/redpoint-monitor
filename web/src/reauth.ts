// 敏感操作的重新验证（设计 17.4，修订第 306 条）：面板返回 403 reauth_required 时弹出密码框，
// 验证通过后自动重试原操作。密码只在提交时发给 /auth/reauth，不在页面里保存。
import { reactive } from 'vue'
import { ApiError, errorText, reauth } from './api'
import { t } from './i18n'

/** 密码框的状态，由 App.vue 中的 ReauthDialog 显示 */
export const reauthState = reactive({ open: false, busy: false, error: '' })

let pending: { resolve: () => void; reject: (e: unknown) => void } | null = null

/** 用户关闭了密码框：调用方据此不再显示错误 */
export class ReauthCancelled extends Error {}

/** 执行 fn；面板要求重新验证时先弹出密码框，验证通过后再执行一次 */
export async function withReauth<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn()
  } catch (e) {
    if (!(e instanceof ApiError && e.code === 'reauth_required')) throw e
    await new Promise<void>((resolve, reject) => {
      pending?.reject(new ReauthCancelled())
      pending = { resolve, reject }
      Object.assign(reauthState, { open: true, busy: false, error: '' })
    })
    return fn()
  }
}

/** 提交密码；成功后关闭密码框并继续原操作 */
export async function submitReauth(password: string) {
  if (!password) {
    reauthState.error = t('请输入登录密码以确认')
    return
  }
  reauthState.busy = true
  reauthState.error = ''
  try {
    await reauth(password)
    reauthState.open = false
    pending?.resolve()
    pending = null
  } catch (e) {
    reauthState.error = errorText(e)
  } finally {
    reauthState.busy = false
  }
}

/** 关闭密码框：原操作不再执行 */
export function cancelReauth() {
  reauthState.open = false
  pending?.reject(new ReauthCancelled())
  pending = null
}
