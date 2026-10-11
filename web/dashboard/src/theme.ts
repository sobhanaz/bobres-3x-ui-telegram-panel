import Aura from '@primeuix/themes/aura'
import { definePreset } from '@primeuix/themes'

// BOBRES look: deep navy surfaces in dark mode, a teal accent, calm and
// legible. Colors are CSS variables, so an operator brand color can be
// applied later (milestone 3) without a rebuild.
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
