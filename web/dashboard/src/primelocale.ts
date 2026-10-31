import { defaultOptions, type PrimeVueLocaleOptions } from 'primevue/config'
import { gregorianLocale, type Lang } from './format'

// PrimeVue's own words (dialog buttons, empty lists, screen-reader labels) in
// the dashboard's language. English is PrimeVue's built-in default.
const en = defaultOptions.locale as PrimeVueLocaleOptions

const fa: PrimeVueLocaleOptions = {
  ...en,
  startsWith: 'شروع با',
  contains: 'شامل',
  notContains: 'شامل نباشد',
  endsWith: 'پایان با',
  equals: 'برابر',
  notEquals: 'نابرابر',
  noFilter: 'بدون فیلتر',
  lt: 'کمتر از',
  lte: 'کمتر یا برابر',
  gt: 'بیشتر از',
  gte: 'بیشتر یا برابر',
  dateIs: 'در تاریخ',
  dateIsNot: 'نه در تاریخ',
  dateBefore: 'پیش از تاریخ',
  dateAfter: 'پس از تاریخ',
  clear: 'پاک کردن',
  apply: 'اعمال',
  matchAll: 'همه شرط‌ها',
  matchAny: 'یکی از شرط‌ها',
  addRule: 'افزودن شرط',
  removeRule: 'حذف شرط',
  accept: 'بله',
  reject: 'خیر',
  choose: 'انتخاب',
  upload: 'بارگذاری',
  cancel: 'انصراف',
  completed: 'انجام شد',
  pending: 'در انتظار',
  dayNames: ['یکشنبه', 'دوشنبه', 'سه‌شنبه', 'چهارشنبه', 'پنجشنبه', 'جمعه', 'شنبه'],
  dayNamesShort: ['ی', 'د', 'س', 'چ', 'پ', 'ج', 'ش'],
  dayNamesMin: ['ی', 'د', 'س', 'چ', 'پ', 'ج', 'ش'],
  monthNames: ['ژانویه', 'فوریه', 'مارس', 'آوریل', 'مه', 'ژوئن', 'ژوئیه', 'اوت', 'سپتامبر', 'اکتبر', 'نوامبر', 'دسامبر'],
  monthNamesShort: ['ژانویه', 'فوریه', 'مارس', 'آوریل', 'مه', 'ژوئن', 'ژوئیه', 'اوت', 'سپتامبر', 'اکتبر', 'نوامبر', 'دسامبر'],
  chooseYear: 'انتخاب سال',
  chooseMonth: 'انتخاب ماه',
  chooseDate: 'انتخاب تاریخ',
  prevDecade: 'دهه قبل',
  nextDecade: 'دهه بعد',
  prevYear: 'سال قبل',
  nextYear: 'سال بعد',
  prevMonth: 'ماه قبل',
  nextMonth: 'ماه بعد',
  prevHour: 'ساعت قبل',
  nextHour: 'ساعت بعد',
  prevMinute: 'دقیقه قبل',
  nextMinute: 'دقیقه بعد',
  prevSecond: 'ثانیه قبل',
  nextSecond: 'ثانیه بعد',
  am: 'ق.ظ',
  pm: 'ب.ظ',
  today: 'امروز',
  weekHeader: 'هفته',
  firstDayOfWeek: 6,
  weak: 'ضعیف',
  medium: 'متوسط',
  strong: 'قوی',
  passwordPrompt: 'رمز را وارد کنید',
  emptyFilterMessage: 'نتیجه‌ای پیدا نشد',
  searchMessage: '{0} نتیجه هست',
  selectionMessage: '{0} مورد انتخاب شد',
  emptySelectionMessage: 'موردی انتخاب نشده',
  emptySearchMessage: 'نتیجه‌ای پیدا نشد',
  fileChosenMessage: '{0} فایل',
  noFileChosenMessage: 'فایلی انتخاب نشده',
  emptyMessage: 'گزینه‌ای نیست',
  aria: {
    ...en.aria,
    trueLabel: 'بله',
    falseLabel: 'خیر',
    nullLabel: 'انتخاب نشده',
    star: '۱ ستاره',
    stars: '{star} ستاره',
    selectAll: 'همه انتخاب شدند',
    unselectAll: 'هیچ‌کدام انتخاب نشده',
    close: 'بستن',
    previous: 'قبلی',
    next: 'بعدی',
    navigation: 'پیمایش',
    scrollTop: 'رفتن به بالا',
    moveTop: 'انتقال به اول',
    moveUp: 'انتقال به بالا',
    moveDown: 'انتقال به پایین',
    moveBottom: 'انتقال به آخر',
    moveToTarget: 'انتقال به مقصد',
    moveToSource: 'انتقال به مبدأ',
    moveAllToTarget: 'انتقال همه به مقصد',
    moveAllToSource: 'انتقال همه به مبدأ',
    pageLabel: 'صفحه {page}',
    firstPageLabel: 'صفحه اول',
    lastPageLabel: 'صفحه آخر',
    nextPageLabel: 'صفحه بعد',
    prevPageLabel: 'صفحه قبل',
    rowsPerPageLabel: 'ردیف در هر صفحه',
    jumpToPageDropdownLabel: 'انتخاب صفحه',
    jumpToPageInputLabel: 'شماره صفحه',
    selectRow: 'ردیف انتخاب شد',
    unselectRow: 'انتخاب ردیف برداشته شد',
    expandRow: 'ردیف باز شد',
    collapseRow: 'ردیف بسته شد',
    showFilterMenu: 'نمایش فیلتر',
    hideFilterMenu: 'بستن فیلتر',
    filterOperator: 'عملگر فیلتر',
    filterConstraint: 'شرط فیلتر',
    editRow: 'ویرایش ردیف',
    saveEdit: 'ذخیره ویرایش',
    cancelEdit: 'لغو ویرایش',
    listView: 'نمای فهرست',
    gridView: 'نمای شبکه‌ای',
    slide: 'اسلاید',
    zoomImage: 'بزرگ‌نمایی تصویر',
    zoomIn: 'بزرگ‌تر',
    zoomOut: 'کوچک‌تر',
    rotateRight: 'چرخش به راست',
    rotateLeft: 'چرخش به چپ',
    listLabel: 'فهرست گزینه‌ها',
  },
}

// The beta languages: the words of the dialogs and lists, and day and month
// names from the browser (machine-translated like the rest of their texts).
const betaWords: Record<'ar' | 'ru' | 'tr', Partial<PrimeVueLocaleOptions>> = {
  ar: {
    accept: 'نعم', reject: 'لا', cancel: 'إلغاء', clear: 'مسح', apply: 'تطبيق', choose: 'اختيار', upload: 'رفع', today: 'اليوم',
    emptyMessage: 'لا توجد خيارات', emptyFilterMessage: 'لا توجد نتائج', emptySearchMessage: 'لا توجد نتائج', noFilter: 'بدون فلتر',
  },
  ru: {
    accept: 'Да', reject: 'Нет', cancel: 'Отмена', clear: 'Очистить', apply: 'Применить', choose: 'Выбрать', upload: 'Загрузить', today: 'Сегодня',
    emptyMessage: 'Нет вариантов', emptyFilterMessage: 'Ничего не найдено', emptySearchMessage: 'Ничего не найдено', noFilter: 'Без фильтра',
  },
  tr: {
    accept: 'Evet', reject: 'Hayır', cancel: 'İptal', clear: 'Temizle', apply: 'Uygula', choose: 'Seç', upload: 'Yükle', today: 'Bugün',
    emptyMessage: 'Seçenek yok', emptyFilterMessage: 'Sonuç bulunamadı', emptySearchMessage: 'Sonuç bulunamadı', noFilter: 'Filtre yok',
  },
}

const betaLocales = new Map<Lang, PrimeVueLocaleOptions>()

function betaLocale(lang: 'ar' | 'ru' | 'tr'): PrimeVueLocaleOptions {
  let l = betaLocales.get(lang)
  if (!l) {
    const loc = gregorianLocale(lang)
    const days = (weekday: 'long' | 'short' | 'narrow') =>
      Array.from({ length: 7 }, (_, i) => new Intl.DateTimeFormat(loc, { weekday, timeZone: 'UTC' }).format(new Date(Date.UTC(2026, 9, 18 + i))))
    const months = (month: 'long' | 'short') =>
      Array.from({ length: 12 }, (_, i) => new Intl.DateTimeFormat(loc, { month, timeZone: 'UTC' }).format(new Date(Date.UTC(2026, i, 1))))
    l = { ...en, ...betaWords[lang], dayNames: days('long'), dayNamesShort: days('short'), dayNamesMin: days('narrow'), monthNames: months('long'), monthNamesShort: months('short') }
    betaLocales.set(lang, l)
  }
  return l
}

export function primeLocale(lang: Lang): PrimeVueLocaleOptions {
  if (lang === 'fa') return fa
  if (lang === 'ar' || lang === 'ru' || lang === 'tr') return betaLocale(lang)
  return en
}
