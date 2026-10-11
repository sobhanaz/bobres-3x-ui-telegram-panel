import Aura from '@primeuix/themes/aura'
import { definePreset, palette, updatePrimaryPalette } from '@primeuix/themes'
import { HEX_RE } from './branding'

// BOBRES look: deep navy surfaces in dark mode, a teal accent, calm and
// legible. Colors are CSS variables, so the operator's brand color is
// applied at runtime (applyBrandColor) without a rebuild.
export const BobresPreset = definePreset(Aura, {
  semantic: {
    primary: {
      50: '{teal.50}',
      100: '{teal.100}',
      200: '{teal.200}',
      300: '{teal.300}',
      400: '{teal.400}',
      500: '{teal.500}',
      600: '{teal.600}',
      700: '{teal.700}',
      800: '{teal.800}',
      900: '{teal.900}',
      950: '{teal.950}',
    },
    colorScheme: {
      light: {
        surface: {
          0: '#ffffff',
          50: '{slate.50}',
          100: '{slate.100}',
          200: '{slate.200}',
          300: '{slate.300}',
          400: '{slate.400}',
          500: '{slate.500}',
          600: '{slate.600}',
          700: '{slate.700}',
          800: '{slate.800}',
          900: '{slate.900}',
          950: '{slate.950}',
        },
      },
      dark: {
        surface: {
          0: '#ffffff',
          50: '#f0f3f9',
          100: '#d9e0ee',
          200: '#b3c0d8',
          300: '#8a9cbf',
          400: '#6178a3',
          500: '#435a86',
          600: '#33466c',
          700: '#253554',
          800: '#18243c',
          900: '#0f1a2e',
          950: '#0a1222',
        },
      },
    },
  },
})

export const darkModeSelector = '.app-dark'

// The brand colour now shown ("" = the preset's teal). Changing the palette
// re-injects PrimeVue's variables, so the same colour is not applied twice.
let appliedColor = ''

/** applyBrandColor makes #rrggbb the primary colour (tints and shades derived
 * from it); empty or invalid goes back to the preset's teal. */
export function applyBrandColor(hex?: string | null): void {
  const next = hex && HEX_RE.test(hex) ? hex.toLowerCase() : ''
  if (next === appliedColor) return
  appliedColor = next
  updatePrimaryPalette(palette(next || '{teal}') as Parameters<typeof updatePrimaryPalette>[0])
}

const defaultIcon = import.meta.env.BASE_URL + 'favicon.svg'

/** applyDocumentBrand sets the browser tab's title and icon (the uploaded
 * logo, or the BOBRES icon). */
export function applyDocumentBrand(title: string, logo?: string | null): void {
  document.title = title
  const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) return
  const href = logo || defaultIcon
  if (link.getAttribute('href') === href) return
  if (logo) link.removeAttribute('type')
  else link.type = 'image/svg+xml'
  link.setAttribute('href', href)
}
