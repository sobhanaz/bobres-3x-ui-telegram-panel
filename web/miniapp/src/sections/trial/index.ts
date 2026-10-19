import { registerSection } from '../../sections'
import TrialView from './TrialView.vue'

registerSection({
  id: 'trial',
  title: 'trial.title',
  hint: 'trial.hint',
  visible: (me) => me.features.trial === true,
  component: TrialView,
  order: 10,
})
