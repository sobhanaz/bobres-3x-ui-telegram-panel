// The reseller section (docs/17-mini-app.md): shown in the More tab to an
// active reseller whose license includes resellers. Core says so in
// me.features.reseller; it checks again on every call.
import { registerSection } from '../../sections'
import './reseller.css'
import ResellerSection from './ResellerSection.vue'

registerSection({
  id: 'reseller',
  title: 'reseller.title',
  hint: 'reseller.hint',
  visible: (me) => me.features.reseller === true,
  component: ResellerSection,
  order: 20,
})
