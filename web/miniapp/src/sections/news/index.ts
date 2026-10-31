import { registerSection } from '../../sections'
import NewsView from './NewsView.vue'

// Product news: the customer's own switch for a private message when the store announces a new product (the same as in the bot). It shows
// while the store has announcements on.
registerSection({
  id: 'news',
  title: 'news.title',
  hint: 'news.hint',
  visible: (me) => me.features.news === true,
  component: NewsView,
  order: 90,
})
