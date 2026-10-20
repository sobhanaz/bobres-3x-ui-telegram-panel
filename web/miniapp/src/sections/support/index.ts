import { registerSection } from '../../sections'
import SupportView from './SupportView.vue'

registerSection({
  id: 'support',
  title: 'support.title',
  hint: 'support.hint',
  visible: (me) => me.features.support === true,
  component: SupportView,
  order: 20,
})
