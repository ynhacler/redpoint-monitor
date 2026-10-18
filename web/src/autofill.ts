// 密码管理器 / 浏览器自动填充的值同步到页面状态（修订第 173 条）。
//
// 问题：自动填充写入输入框时常常不触发 input 事件（Chrome 甚至在用户与页面交互前不让脚本读到填入的密码），
// v-model 的值仍为空——框里看得见内容，点登录却提示“请输入用户名和密码”，密码确认框的按钮一直是灰的。
// 做法：在可能出现自动填充的时机，把输入框里的实际值与上次同步的值不同的，补发一次 input 事件，v-model 随之更新：
//   - change 事件、自动填充时的 CSS 动画（:-webkit-autofill，见 style.css）
//   - 用户的任何交互（pointerdown、keydown、focusin）：Chrome 在此之后才让脚本读到填入的密码
//   - 表单提交前（捕获阶段，先于页面的提交处理）
// 全局生效，各页面的表单不需要单独处理。

const synced = new WeakMap<HTMLInputElement | HTMLTextAreaElement, string>()

function isField(el: EventTarget | null): el is HTMLInputElement | HTMLTextAreaElement {
  if (el instanceof HTMLTextAreaElement) return true
  return el instanceof HTMLInputElement && !['checkbox', 'radio', 'file', 'button', 'submit', 'reset', 'hidden'].includes(el.type)
}

// 输入框的实际值与上次同步的值不同时补发 input 事件
function sync(el: HTMLInputElement | HTMLTextAreaElement) {
  if (synced.get(el) === el.value) return
  synced.set(el, el.value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

function syncWithin(root: ParentNode) {
  root.querySelectorAll('input, textarea').forEach((el) => {
    if (isField(el)) sync(el)
  })
}

/** 安装全局的自动填充同步（main.ts 调用一次） */
export function installAutofillSync() {
  // 页面自己的输入：记下当前值，避免重复补发
  document.addEventListener('input', (e) => {
    if (isField(e.target)) synced.set(e.target, e.target.value)
  }, true)
  document.addEventListener('change', (e) => {
    if (isField(e.target)) sync(e.target)
  }, true)
  document.addEventListener('animationstart', (e) => {
    if (e.animationName === 'vpsmon-autofill' && isField(e.target)) sync(e.target)
  }, true)
  for (const type of ['pointerdown', 'keydown', 'focusin']) {
    document.addEventListener(type, () => syncWithin(document), true)
  }
  document.addEventListener('submit', (e) => {
    if (e.target instanceof HTMLFormElement) syncWithin(e.target)
  }, true)
}
