<div dir="rtl">

# عیب‌یابی

[فهرست راهنما](FA-Home) · [پرسش‌های پرتکرار](FA-FAQ) · [به‌روزرسانی، پشتیبان و بازیابی](FA-Update-Backup-Restore)

اگر چیزی خراب شد، اول نفس بکشید: داده‌های مشتری‌ها در پایگاه داده است و تقریباً هیچ مشکلی آن‌ها را از بین نمی‌برد. این صفحه ابزارها و مشکل‌های رایج را می‌گوید و به راهنمای اجرایی (runbook) هر مشکل در GitHub لینک می‌دهد.

## نگاه اول، برای هر مشکلی

همهٔ دستورها روی سرور اجرا می‌شوند.

۱. وضعیت سرویس‌ها. اگر چیزی پایین باشد، خروجی کد ۱ دارد:

<div dir="ltr">

```bash
sudo bobres status
```

</div>

۲. در داشبورد: **سیستم** ← **سلامت**. نسخه و سلامت هر سرویس، پنل‌های 3x-ui، روش‌های پرداخت و «مشکل‌های باز» اینجاست. **نمای کلی** هم تحویل‌های ناموفق و پرداخت‌های منتظر را نشان می‌دهد.

۳. لاگ‌ها. با `-f` زنده دنبال می‌شوند و با Ctrl+C بسته می‌شوند. نام سرویس را بعد از گزینه‌ها بنویسید (`core`، `bot`، `payments`، `provisioner`، `backup`، `postgres`، `redis`، `caddy`، `tls-agent`):

<div dir="ltr">

```bash
sudo bobres logs -f
```

</div>

<div dir="ltr">

```bash
sudo bobres logs --tail 100 bot
```

</div>

۴. بررسی خود سرور (RAM، دیسک، پورت‌ها، Docker، DNS، گواهی HTTPS):

<div dir="ltr">

```bash
sudo bobres doctor
```

</div>

روی فروشگاهِ در حال کار، دو بررسی پورت ۸۰ و ۴۴۳ شکست می‌خورند چون Caddy آن‌ها را گرفته است. این طبیعی است.

۵. بررسی کل نصب (تنظیمات، سرویس‌ها، دیسک، پشتیبان، توکن ربات، لایسنس، سرورها) با خط‌های PASS و WARN و FAIL:

<div dir="ltr">

```bash
sudo bobres check
```

</div>

۶. اگر باید از پشتیبانی کمک بگیرید، بستهٔ پشتیبانی بسازید:

<div dir="ltr">

```bash
sudo bobres support-bundle
```

</div>

این دستور یک فایل `tar.gz` در `/opt/bobres` می‌سازد: نسخه‌ها، وضعیت، بررسی‌های doctor و آخرین خط‌های لاگ. رمزها و توکن‌ها از آن حذف شده‌اند و پایگاه داده یا پشتیبان در آن نیست. با این حال پیش از فرستادن بخوانیدش (نام کاربری‌ها و شناسه‌های تلگرام ممکن است در لاگ باشند) و بعد از رفع مشکل پاکش کنید. هرگز `.env`، فایل `.bkp` یا کلید پشتیبان را برای کسی نفرستید.

> هرگز `docker volume prune` یا `docker system prune --volumes` نزنید: ممکن است پایگاه داده را پاک کنند. دستور `docker compose config` را هم جلوی دیگران اجرا نکنید، چون رمزها را چاپ می‌کند.

## مشکل‌های رایج

### ربات جواب نمی‌دهد

1. `sudo bobres status`: آیا `bot` بالا و سالم است؟
2. `sudo bobres logs --tail 50 bot`. اگر نوشته «telegram rejected the bot token»، توکن در BotFather باطل شده است.
3. توکن تازه را از BotFather (`/mybots` ← ربات ← API Token) بردارید. بعد `sudo bobres` را بزنید، «Change the bot token» را انتخاب کنید و بچسبانید. منو توکن را با تلگرام می‌سنجد و ربات را دوباره راه می‌اندازد. حساب‌ها و کیف پول مشتری‌ها به شناسهٔ تلگرامشان بسته است، نه به توکن، و از دست نمی‌رود.
4. اگر در لاگ `Conflict: terminated by other getUpdates request` دیدید، برنامهٔ دیگری با همین توکن کار می‌کند (نصب قدیمی، نسخهٔ آزمایشی، یا ربات خود 3x-ui). به BOBRES ربات جدا بدهید.
5. سرور باید به `api.telegram.org` برسد.

در این مدت داشبورد با `sudo bobres admin link` در دسترس است. راهنما: [bot-token-revoked](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/bot-token-revoked.md).

### لینک داشبورد منقضی شده یا کار نمی‌کند

لینک ورود فقط یک بار و حدود ۲ دقیقه کار می‌کند. دوباره در ربات `/dashboard` بفرستید. اگر ربات رد می‌کند، مطمئن شوید حساب شما یک بار `/start` زده است. اگر ربات از کار افتاده، روی سرور:

<div dir="ltr">

```bash
sudo bobres admin link
```

</div>

بدون تلگرام: `sudo bobres admin password` رمز اولیه می‌دهد. کد برنامهٔ احراز هویت را گم کردید؟ `sudo bobres admin link` بدون رمز و کد وارد می‌کند، و بعد در «حساب من» دوباره تنظیمش کنید.

### پنل 3x-ui پاسخ نمی‌دهد

در **نمای کلی** یا **سرورهای پنل** وضعیت «پاسخ نمی‌دهد» می‌آید و خطای آخر نوشته می‌شود. BOBRES خودش سرویس تازه را به پنل‌های سالم دیگر می‌دهد؛ سفارش‌هایی که به همین پنل بسته شده‌اند منتظر می‌مانند و وقتی پنل برگشت تحویل می‌شوند. چیزی از دست نمی‌رود. بررسی کنید: پنل بالاست؟ فایروال پنل IP سرور BOBRES را می‌پذیرد؟ در صفحهٔ سرور «آزمایش اتصال» بزنید. اگر «پنل توکن API را نپذیرفت»، در 3x-ui توکن تازه بسازید و در BOBRES جایگزین کنید. اگر مدتی طول می‌کشد، «سرویس تازه می‌گیرد» را خاموش کنید یا حالت تعمیر را روشن کنید. راهنما: [xui-down](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/xui-down.md).

### سفارش پرداخت شد ولی سرویس نرسید

در **سفارش‌ها و پرداخت‌ها** وضعیت سفارش را ببینید. «تحویل ناموفق» یعنی پول گرفته شده و ساخت سرویس در پنل جواب نداده. اول مشکل پنل را حل کنید، بعد روی سفارش «تلاش دوباره برای تحویل» بزنید. اگر سفارش «منتظر پرداخت» مانده و مشتری می‌گوید پرداخته، راهنمای [payment-stuck](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/payment-stuck.md) و برای تحویل [provisioning-backlog](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/provisioning-backlog.md) را ببینید.

### گواهی HTTPS یا دامنه

مرورگر هشدار گواهی می‌دهد یا داشبورد باز نمی‌شود:

<div dir="ltr">

```bash
sudo bobres ssl status
```

</div>

DNS دامنه باید به IP سرور اشاره کند. نتیجهٔ Caddy را با `sudo bobres logs --tail 100 caddy` ببینید. اگر پس از تغییر گواهی مرورگرها قفل شدند، راه برگشت به گواهی خودکار دامنه این است:

<div dir="ltr">

```bash
sudo bobres ssl auto
```

</div>

راهنما: [https](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/https.md). همچنین ساعت سرور باید درست باشد (`timedatectl`).

### پشتیبان به تلگرام نمی‌رسد

یکی بگیرید و نتیجه را ببینید:

<div dir="ltr">

```bash
sudo bobres backup
```

</div>

دلیل‌های رایج: مالک ربات را مسدود کرده یا هیچ‌وقت `/start` نزده؛ فایل بیش از ۵۰ مگابایت است (پیام می‌گیرید و فایل روی سرور می‌ماند)؛ دیسک پر است؛ توکن ربات باطل شده؛ کپی S3 تنظیم نیست. جزئیات هر دلیل در زبانهٔ **پشتیبان‌ها** و در راهنمای [backups-not-arriving](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/backups-not-arriving.md) آمده. اگر دیسک پر است: [disk-full](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/disk-full.md).

### پیام لایسنس یا «خرید جدید موقتاً در دسترس نیست»

مشتری‌ها این پیام را وقتی می‌بینند که نصب به سقف سرویس فعال سطحش رسیده باشد. `sudo bobres license status` وضعیت و سقف‌ها را نشان می‌دهد ([لایسنس](FA-Licensing)).

### بعد از به‌روزرسانی چیزی خراب شد

`sudo bobres rollback` آخرین به‌روزرسانی را برمی‌گرداند ([به‌روزرسانی، پشتیبان و بازیابی](FA-Update-Backup-Restore)).

## هشدارهای تلگرام

هسته هر دقیقه فروشگاه را بررسی می‌کند و ربات برای مشکل (و پس از رفع آن) به مالک پیام می‌دهد (مدیرها هم، اگر در تنظیمات گیرنده‌شان کرده باشید؛ پشتیبان‌ها هیچ‌وقت): سرویس پایین، پنل بی‌پاسخ، پرداخت گیرکرده، پشتیبان ناموفق یا قدیمی، دیسک پر، گواهی نزدیک به پایان، لایسنس رو به اتمام. مشکل‌های باز در **سیستم** ← **سلامت** هم هست. خاموش کردنش در **تنظیمات** ← «اعلان‌های مدیریت» ← مشکل‌های سیستم است. ربات دربارهٔ خودش نمی‌تواند هشدار بدهد؛ وقتی برگشت، هشدار را با پیام «رفع شد» می‌فرستد.

## راهنماهای اجرایی

فهرست کامل راهنماها با دستورهای دقیق: [docs/runbooks](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/README.md). مهم‌ترین‌ها: [restore](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/restore.md)، [update-rollback](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/update-rollback.md)، [rotate-secrets](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/rotate-secrets.md)، [license](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/license.md) و [monitoring](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/monitoring.md). مشکلی که حل نشد را با بستهٔ پشتیبانی در [issues](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues) گزارش کنید.

</div>
