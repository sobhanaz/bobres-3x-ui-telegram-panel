# Security policy

فارسی در ادامهٔ همین فایل است · Persian follows below.

## Reporting a vulnerability

Please do **not** open a public issue for a security problem.

Report it privately through GitHub: open the repository's **Security** tab and choose **Report a vulnerability**
(https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/security/advisories/new). Only you and the maintainer can
read the report.

Please include:

- what you found and where (the file, the page or the command);
- the steps to reproduce it, and the BOBRES version (`bobres version`);
- what an attacker could do with it.

Please do not test against a store that is not yours, do not read or change other people's data, and do not publish the
problem before it is fixed.

The maintainer handles reports on a best-effort basis; there is no promised response time yet. A fix ships as a new
release, and the release notes say what was fixed.

## Supported versions

Only the latest release is supported. Update with `sudo bobres update`.

## What is in scope

BOBRES's own code and deployment files in this repository: the core, bot, payments, provisioner, backup and
license-server programs, the dashboard, the installer and the `bobres` command. Problems in Docker, Postgres, Redis,
Caddy, Telegram or 3x-ui themselves belong to those projects.

---

# سیاست امنیتی

## گزارش یک آسیب‌پذیری

لطفاً برای مشکل امنیتی **مسئلهٔ عمومی (public issue) باز نکنید**.

گزارش را به‌صورت خصوصی از راه گیت‌هاب بفرستید: در مخزن، زبانهٔ **Security** را باز کنید و **Report a vulnerability** را
بزنید (https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/security/advisories/new). فقط شما و نگه‌دارندهٔ پروژه
گزارش را می‌بینید.

لطفاً این‌ها را بنویسید:

- چه چیزی پیدا کرده‌اید و کجا (فایل، صفحه یا دستور)؛
- مراحل تکرار مشکل و نسخهٔ BOBRES (`bobres version`)؛
- اینکه یک مهاجم با آن چه کاری می‌تواند انجام دهد.

لطفاً روی فروشگاهی که متعلق به شما نیست آزمایش نکنید، دادهٔ دیگران را نخوانید و تغییر ندهید، و پیش از رفع مشکل آن را منتشر
نکنید.

نگه‌دارندهٔ پروژه گزارش‌ها را تا جایی که بتواند بررسی می‌کند؛ هنوز زمانی برای پاسخ قول داده نشده است. رفع مشکل در یک
نسخهٔ تازه منتشر می‌شود و یادداشت‌های نسخه می‌گویند چه چیزی اصلاح شده است.

## نسخه‌های پشتیبانی‌شده

فقط آخرین نسخه پشتیبانی می‌شود. با `sudo bobres update` به‌روز کنید.

## چه چیزی در محدوده است

کد و فایل‌های استقرار خودِ BOBRES در همین مخزن: برنامه‌های هسته (core)، ربات، پرداخت‌ها، تحویل سرویس
(provisioner)، پشتیبان‌گیری و سرور لایسنس، داشبورد، نصب‌کننده و دستور `bobres`. مشکلات خودِ Docker، Postgres، Redis،
Caddy، تلگرام یا 3x-ui به همان پروژه‌ها مربوط است.
