import { registerSection } from '../../sections'
import InviteView from './InviteView.vue'

registerSection({
  id: 'invite',
  title: 'invite.title',
  hint: 'invite.hint',
  visible: (me) => me.features.invite === true,
  component: InviteView,
  order: 30,
})
