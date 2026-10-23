import { registerSection } from '../../sections'
import DigitalView from './DigitalView.vue'

registerSection({
  id: 'digital',
  title: 'digital.title',
  hint: 'digital.hint',
  visible: (me) => me.features.digital === true,
  component: DigitalView,
  order: 5,
})
