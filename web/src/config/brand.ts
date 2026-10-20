/**
 * Single source for identity, contact, locale and theme. Every page imports
 * this; nothing else in the app defines these values directly.
 */

export const brand = {
  identity: {
    name: 'nmail',
    shortName: 'nmail',
    tagline: 'One identity. Every inbox.',
    domain: import.meta.env.VITE_PERSONAL_EMAIL_DOMAIN || 'nmail.local',
    adminTitle: 'nmail Admin',
  },
  contact: {
    supportEmail: 'support@nmail.local',
  },
  theme: {
    light: 'brand' as const,
    dark: 'brand-dark' as const,
  },
  artwork: {
    logoMark: '/logo-mark.svg',
  },
} as const

export type Brand = typeof brand
