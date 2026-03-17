import { initializeMermaid } from '@md/core/utils'
import { createPinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'

import { setupComponents } from './utils/setup-components'
import { detectAndSetupStorageEngine } from './utils/storage'

import 'vue-sonner/style.css'

/* 每个页面公共css */
import '@/assets/index.css'
import '@/assets/less/theme.less'

// 异步初始化 mermaid，避免初始化顺序问题
initializeMermaid().catch(console.error)

setupComponents()

// 检测服务器模式并切换存储引擎，然后再挂载应用
detectAndSetupStorageEngine().then(() => {
  const app = createApp(App)
  app.use(createPinia())
  app.mount(`#app`)
})
