import { registerSection } from '../../sections'
import CodesView from './CodesView.vue'

registerSection({
  id: 'codes',
  title: 'codes.title',
  hint: 'codes.hint',
  visible: (me) => me.features.codes === true,
  component: CodesView,
  order: 12,
})
