// LAB ONLY: drives the local Docker lab (deploy/lab) with headless Chrome. The login link's token stays in this process.
import puppeteer from 'puppeteer-core'
import { execSync } from 'node:child_process'

export const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
export const BASE = 'https://bobres.localhost:8443'

export async function launch(extra = {}) {
  return puppeteer.launch({
    executablePath: CHROME,
    headless: true,
    args: [
      '--host-resolver-rules=MAP bobres.localhost 127.0.0.1',
      '--ignore-certificate-errors',
      '--no-first-run',
      '--hide-scrollbars',
      '--font-render-hinting=none',
    ],
    ...extra,
  })
}

// A fresh one-time login link; the token lives only in this process.
function loginUrl() {
  const out = execSync("docker exec bobres-lab-bot bobres admin link", { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] })
  const m = out.match(/https:\/\/\S+#t=[A-Za-z0-9_-]+/)
  if (!m) throw new Error('no login link')
  return m[0]
}

export async function login(page, lang = 'en', theme = 'dark') {
  await page.evaluateOnNewDocument((lang, theme) => {
    try {
      if (!localStorage.getItem('bobres.lang')) localStorage.setItem('bobres.lang', lang)
      if (!localStorage.getItem('bobres.theme')) localStorage.setItem('bobres.theme', theme)
    } catch {}
  }, lang, theme)
  await page.goto(loginUrl(), { waitUntil: 'networkidle2' })
  await new Promise((r) => setTimeout(r, 1500))
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
