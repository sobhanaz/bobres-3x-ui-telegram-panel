// The dashboard's 23 sections (docs/04-dashboard.md), the permission each
// needs, and the milestone that builds it. The menu shows a section only to
// staff whose role grants its permission.

export interface Section {
  key: string
  path: string
  icon: string
  perm: string
  /** A second need besides the permission: the vendor's own features (selling licenses, the supplier resale) appear only for an
   * owner whose store's license grants them (core adds `private.features` to the permission list then). */
  needs?: string
  group: 'store' | 'customers' | 'sales' | 'setup' | 'admin'
  milestone: number
}

export const sections: Section[] = [
  { key: 'overview', path: '', icon: 'pi pi-chart-bar', perm: 'overview.read', group: 'store', milestone: 1 },
  { key: 'users', path: 'users', icon: 'pi pi-users', perm: 'users.read', group: 'customers', milestone: 2 },
  { key: 'services', path: 'services', icon: 'pi pi-server', perm: 'services.read', group: 'customers', milestone: 2 },
  { key: 'support', path: 'support', icon: 'pi pi-comments', perm: 'tickets.write', group: 'customers', milestone: 4 },
  { key: 'broadcasts', path: 'broadcasts', icon: 'pi pi-megaphone', perm: 'broadcasts.write', group: 'customers', milestone: 4 },
  { key: 'plans', path: 'plans', icon: 'pi pi-box', perm: 'plans.write', group: 'sales', milestone: 2 },
  { key: 'payments', path: 'payments', icon: 'pi pi-credit-card', perm: 'payments.read', group: 'sales', milestone: 2 },
  { key: 'ledger', path: 'ledger', icon: 'pi pi-wallet', perm: 'ledger.read', group: 'sales', milestone: 2 },
  { key: 'reports', path: 'reports', icon: 'pi pi-chart-line', perm: 'reports.read', group: 'sales', milestone: 7 },
  { key: 'discounts', path: 'discounts', icon: 'pi pi-percentage', perm: 'discounts.write', group: 'sales', milestone: 2 },
  { key: 'giftcodes', path: 'gift-codes', icon: 'pi pi-gift', perm: 'discounts.write', group: 'sales', milestone: 8 },
  { key: 'licenses', path: 'licenses', icon: 'pi pi-key', perm: 'license.write', needs: 'private.features', group: 'sales', milestone: 8 },
  { key: 'supplier', path: 'supplier', icon: 'pi pi-shopping-bag', perm: 'gateways.write', needs: 'private.features', group: 'sales', milestone: 8 },
  { key: 'affiliates', path: 'affiliates', icon: 'pi pi-share-alt', perm: 'license.write', needs: 'private.features', group: 'sales', milestone: 8 },
  { key: 'hosted', path: 'hosted', icon: 'pi pi-cloud', perm: 'license.write', needs: 'private.features', group: 'admin', milestone: 8 },
  { key: 'resellers', path: 'resellers', icon: 'pi pi-sitemap', perm: 'resellers.write', group: 'sales', milestone: 5 },
  { key: 'branding', path: 'branding', icon: 'pi pi-palette', perm: 'branding.write', group: 'setup', milestone: 3 },
  { key: 'settings', path: 'settings', icon: 'pi pi-cog', perm: 'settings.write', group: 'setup', milestone: 3 },
  { key: 'servers', path: 'servers', icon: 'pi pi-database', perm: 'servers.write', group: 'setup', milestone: 6 },
  { key: 'gateways', path: 'gateways', icon: 'pi pi-building-columns', perm: 'gateways.write', group: 'setup', milestone: 6 },
  { key: 'staff', path: 'staff', icon: 'pi pi-id-card', perm: 'staff.write', group: 'admin', milestone: 3 },
  { key: 'system', path: 'system', icon: 'pi pi-desktop', perm: 'system.read', group: 'admin', milestone: 6 },
  { key: 'audit', path: 'audit', icon: 'pi pi-history', perm: 'audit.read', group: 'admin', milestone: 3 },
]

export const groups: Section['group'][] = ['store', 'customers', 'sales', 'setup', 'admin']

/** visibleSections are the sections a staff member's permissions open. */
export function visibleSections(perms: readonly string[]): Section[] {
  return sections.filter((s) => perms.includes(s.perm) && (!s.needs || perms.includes(s.needs)))
}
