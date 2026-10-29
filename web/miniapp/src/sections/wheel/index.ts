import { registerSection } from '../../sections'
import WheelView from './WheelView.vue'

registerSection({
  id: 'wheel',
  title: 'wheel.title',
  hint: 'wheel.hint',
  visible: (me) => me.features.wheel === true,
  component: WheelView,
  order: 25,
})
