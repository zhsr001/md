import SparkMD5 from 'spark-md5'
import { ref } from 'vue'
import { toBase64 } from '@/utils'
import { fileUpload } from '@/utils/file'
import { store } from '@/utils/storage'

const STORAGE_KEY = 'uploaded_image_map'

export function useImageUploader() {
  const isUploading = ref(false)
  const error = ref<string | null>(null)

  // 获取本地缓存
  const getStorageMap = async (): Promise<Record<string, string>> => {
    try {
      return await store.getJSON<Record<string, string>>(STORAGE_KEY, {})
    }
    catch {
      return {}
    }
  }

  // 更新本地缓存
  const updateStorageMap = async (hash: string, url: string) => {
    const map = await getStorageMap()
    map[hash] = url
    await store.setJSON(STORAGE_KEY, map)
  }

  // 计算 Blob/File 的 MD5
  const calculateHash = (file: Blob): Promise<string> => {
    return new Promise((resolve, reject) => {
      const fileReader = new FileReader()
      const spark = new SparkMD5.ArrayBuffer()

      fileReader.onload = (e) => {
        if (e.target?.result) {
          spark.append(e.target.result as ArrayBuffer)
          resolve(spark.end())
        }
        else {
          reject(new Error('文件读取失败'))
        }
      }
      fileReader.onerror = () => reject(new Error('文件读取错误'))
      fileReader.readAsArrayBuffer(file)
    })
  }

  // URL 转 File (需注意 CORS)
  const urlToFile = async (url: string): Promise<File> => {
    // 提取文件名
    const getFilename = (u: string) => u.split('/').pop()?.split('?')[0] || `image-${Date.now()}.png`
    const filename = getFilename(url)

    // 内部函数：尝试获取 Blob
    const fetchBlob = async (targetUrl: string, options?: RequestInit) => {
      const res = await fetch(targetUrl, options)
      if (!res.ok)
        throw new Error(`Status: ${res.status}`)
      return await res.blob()
    }

    try {
      // 1. 尝试直接请求 (设置 no-referrer 以尝试绕过部分防盗链)
      const blob = await fetchBlob(url, { referrerPolicy: 'no-referrer' })
      return new File([blob], filename, { type: blob.type })
    }
    catch (directErr) {
      console.warn(`Direct fetch failed for ${url}, trying proxy...`, directErr)

      // 2. 失败后尝试通过 wsrv.nl 代理请求
      try {
        const proxyUrl = `https://wsrv.nl/?url=${encodeURIComponent(url)}`
        const blob = await fetchBlob(proxyUrl)
        return new File([blob], filename, { type: blob.type })
      }
      catch (proxyErr: any) {
        // 3. 代理也失败，抛出异常
        console.error(`Proxy fetch failed for ${url}`, proxyErr)
        const isCors = proxyErr.message.includes('Failed to fetch') || proxyErr.name === 'TypeError'
        const msg = isCors
          ? '跨域请求失败：目标图片禁止了跨域访问，且代理服务也无法获取。'
          : `图片下载失败: ${proxyErr.message}`
        throw new Error(msg)
      }
    }
  }

  // 核心上传方法
  const upload = async (resource: string | File): Promise<string> => {
    isUploading.value = true
    error.value = null

    try {
      let file: File
      if (typeof resource === 'string') {
        file = await urlToFile(resource)
      }
      else {
        file = resource
      }

      // 1. 计算 Hash
      const hash = await calculateHash(file)
      console.log('File Hash:', hash)

      // 2. 检查缓存
      const cache = await getStorageMap()
      if (cache[hash]) {
        console.log('⚡️ 命中缓存，跳过上传')
        return cache[hash]
      }

      // 3. 准备上传：转换 Base64 (fileUpload 需要)
      const base64Content = await toBase64(file)

      // 4. 调用项目现有 API 上传
      console.log('🚀 调用 fileUpload 上传...')
      const url = await fileUpload(base64Content, file)

      // 5. 写入缓存
      if (url) {
        await updateStorageMap(hash, url)
      }

      return url
    }
    catch (err: any) {
      console.error(err)
      const msg = err.message || '上传失败'
      error.value = msg
      throw new Error(msg)
    }
    finally {
      isUploading.value = false
    }
  }

  return { upload, isUploading, error }
}
