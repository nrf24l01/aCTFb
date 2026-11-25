#!/usr/bin/env node
// Real-browser verification for the status badge colours.
//
// Loads the test-only visual fixture served by one owned Vite dev server,
// measures computed styles (not the CSS source), asserts WCAG AA text contrast,
// and writes screenshots. Exits non-zero on the first failed assertion so a
// broken render cannot be reported as a pass.
import { chromium } from 'playwright-core'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'

const BASE_URL = process.env.BASE_URL
const OUT = process.env.OUT ?? '/tmp/opencode/status-colors'
if (!BASE_URL) {
  console.error('BASE_URL is required')
  process.exit(2)
}
mkdirSync(OUT, { recursive: true })

const EXECUTABLE = process.env.CHROMIUM_PATH
  ?? ['/usr/bin/chromium', '/usr/bin/chromium-browser', '/usr/bin/google-chrome-stable'].find((candidate) =>
    existsSync(candidate),
  )
if (!EXECUTABLE) {
  console.error('no Chromium executable found')
  process.exit(2)
}
console.log(`chromium: ${EXECUTABLE}`)

const EXPECTED_TONE = {
  ACCEPTED: 'success',
  SOLVED: 'success',
  PASSED: 'success',
  ACTIVE: 'active',
  RUNNING: 'active',
  CHECKING: 'active',
  PENDING: 'attention',
  PENDING_REVIEW: 'attention',
  SCHEDULED: 'attention',
  REJECTED: 'error',
  ERROR: 'error',
  FAILED: 'error',
  LOCKED: 'restricted',
  RESTRICTED: 'restricted',
  DRAFT: 'neutral',
  PROVIDER_TIMEOUT: 'neutral',
  FINISHED: 'muted',
  EXPIRED: 'muted',
  CANCELED: 'muted',
  // legacy tone props
  ok: 'success',
  look: 'attention',
  blocked: 'restricted',
  broken: 'error',
  unknown: 'neutral',
  off: 'neutral',
  running: 'active',
  gone: 'muted',
}

const EXPECTED_LABEL = {
  PENDING_REVIEW: 'Needs review',
  PROVIDER_TIMEOUT: 'Provider timeout',
  CANCELED: 'Canceled',
  EXPIRED: 'Expired',
  RESTRICTED: 'Restricted',
}

function srgbToLinear(channel) {
  const c = channel / 255
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}

function parseColor(value) {
  const raw = (value ?? '').trim()
  const hex = /^#([0-9a-f]{6})$/i.exec(raw)
  if (hex) {
    const int = Number.parseInt(hex[1], 16)
    return { r: (int >> 16) & 255, g: (int >> 8) & 255, b: int & 255, a: 1 }
  }
  const match = /rgba?\(([^)]+)\)/.exec(raw)
  if (!match) throw new Error(`cannot parse colour: ${value}`)
  const parts = match[1].split(/[,\s/]+/).filter(Boolean).map(Number)
  return { r: parts[0], g: parts[1], b: parts[2], a: parts[3] ?? 1 }
}

function luminance({ r, g, b }) {
  return 0.2126 * srgbToLinear(r) + 0.7152 * srgbToLinear(g) + 0.0722 * srgbToLinear(b)
}

function contrast(fg, bg) {
  const l1 = luminance(parseColor(fg))
  const l2 = luminance(parseColor(bg))
  const hi = Math.max(l1, l2)
  const lo = Math.min(l1, l2)
  return (hi + 0.05) / (lo + 0.05)
}

const failures = []
const measurements = []

function check(condition, message) {
  if (!condition) failures.push(message)
}

const browser = await chromium.launch({
  executablePath: EXECUTABLE,
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})

try {
  // ---- Desktop (1440x900, DPR 1) -------------------------------------------
  const desktop = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await desktop.newPage()
  await page.goto(BASE_URL, { waitUntil: 'load' })
  await page.waitForSelector('[data-slot="badge"]')

  const tokens = await page.evaluate(() => {
    const root = getComputedStyle(document.documentElement)
    const tones = ['success', 'active', 'attention', 'error', 'restricted', 'neutral', 'muted']
    return Object.fromEntries(
      tones.map((tone) => [
        tone,
        {
          ink: root.getPropertyValue(`--status-${tone}-ink`).trim(),
          surface: root.getPropertyValue(`--status-${tone}-surface`).trim(),
          border: root.getPropertyValue(`--status-${tone}-border`).trim(),
        },
      ]),
    )
  })

  const raw = await page.evaluate(() => {
    const badges = Array.from(document.querySelectorAll('[data-slot="badge"]'))
    return badges.map((badge) => {
      const style = getComputedStyle(badge)
      const label = badge.querySelector('span')
      const icon = badge.querySelector('svg')
      const wrapper = badge.closest('[data-tone]')
      const surface = badge.closest('[data-surface]')?.getAttribute('data-surface') ?? 'unknown'
      const animated = [badge, ...badge.querySelectorAll('*')].filter(
        (el) => getComputedStyle(el).animationName !== 'none',
      ).length
      return {
        key: wrapper?.getAttribute('data-tone') ?? '',
        kind: wrapper?.closest('[data-kind="legacy"]') ? 'legacy' : 'status',
        surface,
        text: badge.textContent.trim(),
        color: style.color,
        backgroundColor: style.backgroundColor,
        borderColor: style.borderColor,
        borderStyle: style.borderStyle,
        borderWidth: style.borderWidth,
        animationName: style.animationName,
        animatedDescendants: animated,
        labelWidth: label ? label.getBoundingClientRect().width : 0,
        iconCount: icon ? 1 : 0,
        iconAriaHidden: icon?.getAttribute('aria-hidden') ?? null,
        iconWidth: icon ? icon.getBoundingClientRect().width : 0,
        badgeWidth: badge.getBoundingClientRect().width,
        badgeHeight: badge.getBoundingClientRect().height,
        overflowHidden: style.overflow,
        fontFamily: style.fontFamily,
      }
    })
  })

  check(raw.length >= 60, `expected at least 60 badges, got ${raw.length}`)

  // Every fixture key must actually be rendered and measured. A dropped or
  // renamed badge would otherwise shrink the sample and still pass a count floor.
  const measuredKeys = new Set(raw.map((item) => item.key))
  for (const key of Object.keys(EXPECTED_TONE)) {
    check(measuredKeys.has(key), `expected status/tone "${key}" was not measured`)
  }
  for (const key of Object.keys(EXPECTED_LABEL)) {
    check(measuredKeys.has(key), `expected label case "${key}" was not measured`)
  }

  for (const item of raw) {
    const expectedTone = EXPECTED_TONE[item.key]
    check(Boolean(expectedTone), `fixture badge ${item.key} has no expected tone`)
    const token = tokens[expectedTone]
    const ratio = contrast(item.color, item.backgroundColor)
    const expectedLabel = EXPECTED_LABEL[item.key]
    measurements.push({ ...item, expectedTone, contrast: Number(ratio.toFixed(2)) })

    check(
      item.color === toRgb(token.ink),
      `${item.key}: ink ${item.color} != token ${toRgb(token.ink)}`,
    )
    check(
      item.backgroundColor === toRgb(token.surface),
      `${item.key}: surface ${item.backgroundColor} != token ${toRgb(token.surface)}`,
    )
    check(
      item.borderColor === toRgb(token.border),
      `${item.key}: border ${item.borderColor} != token ${toRgb(token.border)}`,
    )
    check(item.borderStyle === 'solid' && parseFloat(item.borderWidth) >= 1, `${item.key}: border not rendered (${item.borderStyle} ${item.borderWidth})`)
    check(ratio >= 4.5, `${item.key}: contrast ${ratio.toFixed(2)} < 4.5`)
    check(item.text.length > 0, `${item.key}: empty label`)
    if (expectedLabel) check(item.text === expectedLabel, `${item.key}: label "${item.text}" != "${expectedLabel}"`)
    check(item.labelWidth > 0, `${item.key}: label not laid out`)
    check(item.iconCount === 1, `${item.key}: missing icon`)
    check(item.iconAriaHidden === 'true', `${item.key}: icon is not aria-hidden`)
    check(item.iconWidth >= 8, `${item.key}: icon not visible (${item.iconWidth}px)`)
    check(item.animationName === 'none' && item.animatedDescendants === 0, `${item.key}: animated`)
    check(item.badgeHeight >= 18, `${item.key}: badge collapsed (${item.badgeHeight}px)`)
  }

  // Grouped summary: worst contrast per tone.
  const byTone = {}
  for (const item of measurements) {
    byTone[item.expectedTone] ??= []
    byTone[item.expectedTone].push(item.contrast)
  }
  for (const [tone, values] of Object.entries(byTone)) {
    console.log(`${tone.padEnd(11)} n=${String(values.length).padStart(3)} min=${Math.min(...values).toFixed(2)}:1`)
  }

  // Unknown status must be neutral, and expired/canceled must not be active.
  const unknown = measurements.find((m) => m.key === 'PROVIDER_TIMEOUT')
  check(unknown?.expectedTone === 'neutral', 'unknown status is not neutral')
  for (const key of ['EXPIRED', 'CANCELED', 'FINISHED']) {
    const item = measurements.find((m) => m.key === key && m.kind === 'status')
    check(item?.expectedTone === 'muted', `${key} is not muted`)
  }

  // Desktop page must not overflow horizontally.
  const desktopOverflow = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    innerWidth: window.innerWidth,
  }))
  check(
    desktopOverflow.scrollWidth <= desktopOverflow.innerWidth + 1,
    `desktop overflow ${desktopOverflow.scrollWidth} > ${desktopOverflow.innerWidth}`,
  )

  await page.screenshot({ path: path.join(OUT, 'desktop-1440.png'), fullPage: true })

  // ---- Desktop at 200% (CSS zoom) -----------------------------------------
  await page.evaluate(() => {
    document.documentElement.style.zoom = '2'
  })
  await page.waitForTimeout(150)
  const zoomed = await page.evaluate(() => {
    const badge = document.querySelector('[data-slot="badge"]')
    const style = getComputedStyle(badge)
    const label = badge.querySelector('span')
    return {
      color: style.color,
      backgroundColor: style.backgroundColor,
      borderColor: style.borderColor,
      labelWidth: label.getBoundingClientRect().width,
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
    }
  })
  check(contrast(zoomed.color, zoomed.backgroundColor) >= 4.5, 'zoomed contrast < 4.5')
  check(zoomed.labelWidth > 0, 'zoomed label not laid out')
  check(
    zoomed.scrollWidth <= zoomed.innerWidth + 1,
    `200% zoom overflow ${zoomed.scrollWidth} > ${zoomed.innerWidth}`,
  )
  await page.screenshot({ path: path.join(OUT, 'desktop-1440-zoom200.png'), fullPage: true })
  await page.evaluate(() => {
    document.documentElement.style.zoom = '1'
  })

  await desktop.close()

  // ---- Mobile 375x812, DPR 2 ---------------------------------------------
  const mobile = await browser.newContext({
    viewport: { width: 375, height: 812 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
  })
  const mobilePage = await mobile.newPage()
  await mobilePage.goto(BASE_URL, { waitUntil: 'load' })
  await mobilePage.waitForSelector('[data-slot="badge"]')

  const mobileResult = await mobilePage.evaluate(() => {
    const badges = Array.from(document.querySelectorAll('[data-slot="badge"]'))
    const clipped = badges
      .filter((badge) => {
        const label = badge.querySelector('span')
        return label ? label.scrollWidth > label.clientWidth + 1 : false
      })
      .map((badge) => badge.textContent.trim())
    const offscreen = badges
      .filter((badge) => {
        const rect = badge.getBoundingClientRect()
        return rect.left < -1 || rect.right > window.innerWidth + 1
      })
      .map((badge) => badge.textContent.trim())
    const first = getComputedStyle(badges[0])
    return {
      count: badges.length,
      clipped,
      offscreen,
      color: first.color,
      backgroundColor: first.backgroundColor,
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
    }
  })

  check(mobileResult.count === raw.length, `mobile badge count ${mobileResult.count} != ${raw.length}`)
  check(mobileResult.clipped.length === 0, `mobile clipped labels: ${mobileResult.clipped.join(', ')}`)
  check(mobileResult.offscreen.length === 0, `mobile offscreen badges: ${mobileResult.offscreen.join(', ')}`)
  check(
    mobileResult.scrollWidth <= mobileResult.innerWidth + 1,
    `mobile overflow ${mobileResult.scrollWidth} > ${mobileResult.innerWidth}`,
  )
  check(contrast(mobileResult.color, mobileResult.backgroundColor) >= 4.5, 'mobile contrast < 4.5')

  await mobilePage.screenshot({ path: path.join(OUT, 'mobile-375.png'), fullPage: true })
  await mobile.close()

  writeFileSync(
    path.join(OUT, 'measurements.json'),
    JSON.stringify({ tokens, measurements, mobile: mobileResult, failures }, null, 2),
  )

  console.log(`badges measured: ${measurements.length} (desktop + 375px)`)
  console.log(`screenshots: ${OUT}`)
} finally {
  await browser.close()
}

if (failures.length > 0) {
  console.error(`FAILED ${failures.length} check(s):`)
  for (const failure of failures.slice(0, 40)) console.error(` - ${failure}`)
  process.exit(1)
}
console.log('PASS: all computed-style and contrast assertions held')

function toRgb(value) {
  const { r, g, b, a } = parseColor(value)
  return a === 1 ? `rgb(${r}, ${g}, ${b})` : `rgba(${r}, ${g}, ${b}, ${a})`
}
