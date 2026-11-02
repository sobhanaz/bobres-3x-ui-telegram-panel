// Config names (docs/17-mini-app.md, "Config names"): instead of the automatic name, a customer (or a reseller) may
// choose the name of a new service's config and rename one they have, when the store has it on for them
// (me.features.config_names). Core checks the rules, the reseller's prefix and that the name is free on every panel; a
// name it does not take is an answer with a reason, not an error. Renewals and top-ups never carry a name.
import { ApiError, call, type Service } from '../../api'
import { errorKey } from '../../errors'
import { tr, type Key } from '../../i18n'
import { state } from '../../store'

/** NameCheck is core's answer: ok, or why not; name is the full name the config gets (a reseller's prefix in front). */
export interface NameCheck { ok: boolean; reason: string; name: string }

/** RenameResult is a rename's answer: ok with the service as it is now, or why not. */
export interface RenameResult extends NameCheck { service?: Service }

export const namesApi = {
  /** check asks whether a name can be used for a new service, or (subscriptionId) as the new name of one of the user's. */
  check: (name: string, subscriptionId?: string) =>
    call<NameCheck>('POST', '/config-name/check', subscriptionId ? { name, subscription_id: subscriptionId } : { name }),
  rename: (id: string, name: string) => call<RenameResult>('POST', `/services/${id}/rename`, { name }),
}

/** namesOn says whether the store lets this user choose config names. */
export const namesOn = (): boolean => state.me?.features.config_names === true

const reasons = ['off', 'length', 'chars', 'mix', 'automatic', 'taken', 'unchecked', 'not_renamable']

/** reasonText words why a name cannot be used; an unknown reason reads as one that could not be checked. */
export function reasonText(reason: string): string {
  return tr((reasons.includes(reason) ? `names.reason.${reason}` : 'names.reason.unchecked') as Key)
}

/** nameError words a failed call: a refused name (400 config_name) by its reason, anything else as the shop does. */
export function nameError(e: unknown): string {
  if (e instanceof ApiError && e.code === 'config_name') return reasonText(e.reason)
  return tr(errorKey(e))
}
