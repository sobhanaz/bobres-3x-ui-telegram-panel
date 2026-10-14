#!/usr/bin/env python3
r"""Fills the LOCAL DOCKER LAB with fake demo data, for the dashboard screenshots.

LAB ONLY. It writes straight into the lab's database (SQL, no events, so no Telegram message
goes out) and it renames the owner, so take `bobres backup` first and put the lab back with
`bobres restore <that backup> --yes` afterwards (docs/marketing/README.md).

    OWNER=$(./lab-psql.sh "select id from core.users where role = 'owner'" | sed -n 3p | tr -d ' ')
    PLAN=$(./lab-psql.sh "select id from core.plans limit 1" | sed -n 3p | tr -d ' ')
    SERVER=$(./lab-psql.sh "select id from provisioner.xui_servers order by created_at limit 1" | sed -n 3p | tr -d ' ')
    python3 seed-demo.py "$OWNER" "$PLAN" "$SERVER" > out/seed.sql && ./lab-psql.sh "$(cat out/seed.sql)"

It prints SQL. Everything in it is made up: usernames start with "demo_", Telegram ids
are in an unused range, card and wallet addresses are placeholders. Arguments: the owner's user id, the
id of the existing plan to reuse as the trial plan and the id of the lab's one 3x-ui server (all read from the
lab's database first).

The counts are chosen so that no English label reads "1 days", "1 resellers" or "1 services on 1 servers" (the
dashboard does not use plurals): a trial of three days, two resellers and two servers (the second one is a copy
of the lab's, so it answers) with the running services spread over them. Take the screenshots within ten
minutes of seeding: after that the core's usage sync asks the panel about the made-up services.
"""
import json
import random
import sys
import uuid

if len(sys.argv) != 4:
    sys.exit("usage: seed-demo.py <owner user id> <existing plan id> <existing 3x-ui server id>")
OWNER, OLD_PLAN, SERVER = sys.argv[1], sys.argv[2], sys.argv[3]

rng = random.Random(20261014)
GB = 1 << 30
out = []


def q(s):
    return "'" + str(s).replace("'", "''") + "'"


def ago(seconds):
    return f"(now() - interval '{int(seconds)} seconds')"


def ins(table, **cols):
    keys = ", ".join(cols)
    vals = ", ".join("NULL" if v is None else v if isinstance(v, Raw) else q(v) if isinstance(v, str) else str(v) for v in cols.values())
    out.append(f"INSERT INTO {table} ({keys}) VALUES ({vals});")


class Raw(str):
    pass


def uid():
    return str(uuid.UUID(int=rng.getrandbits(128), version=4))


def fixed(name):
    """An id that does not draw from rng: ids added later must not shift the data the pictures were made from."""
    return str(uuid.uuid5(uuid.NAMESPACE_URL, "bobres-demo/" + name))


DAY = 86400
# --- the owner: a fake name for the screenshots, and the old test orders out of the way --------------
out.append(f"UPDATE core.users SET username = 'demo_owner' WHERE id = {q(OWNER)};")
out.append("UPDATE core.orders SET created_at = created_at - interval '90 days', updated_at = updated_at - interval '90 days';")
out.append("UPDATE core.subscriptions SET created_at = created_at - interval '90 days', updated_at = updated_at - interval '90 days';")

# --- plans -------------------------------------------------------------------------------------
plans = {}


def plan(key, fa, en, kind, days, gb, price, **kw):
    pid = OLD_PLAN if key == "trial" else uid()
    plans[key] = dict(id=pid, days=days, bytes=(gb * GB if gb else None), price=price, kind=kind)
    cols = dict(name_i18n=Raw(q(json.dumps({"fa": fa, "en": en}, ensure_ascii=False)) + "::jsonb"), kind=kind,
                duration_days=days, traffic_bytes=(gb * GB if gb else None), price=price, currency="IRT",
                enabled=True, is_trial=kw.get("trial", False), sort=kw["sort"], is_topup=kw.get("topup", False))
    if key == "trial":
        sets = ", ".join(f"{k} = " + ("NULL" if v is None else v if isinstance(v, Raw) else q(v) if isinstance(v, str) else ("true" if v is True else "false" if v is False else str(v))) for k, v in cols.items())
        out.append(f"UPDATE core.plans SET {sets} WHERE id = {q(pid)};")
    else:
        ins("core.plans", id=pid, **cols)


plan("trial", "آزمایشی رایگان", "Free trial", "both", 3, 1, 0, trial=True, sort=1)
plan("p10", "۱۰ گیگ · ۳۰ روزه", "10 GB · 30 days", "both", 30, 10, 60000, sort=2)
plan("p30", "۳۰ گیگ · ۳۰ روزه", "30 GB · 30 days", "both", 30, 30, 120000, sort=3)
plan("p100", "۱۰۰ گیگ · ۳۰ روزه", "100 GB · 30 days", "both", 30, 100, 260000, sort=4)
plan("punl", "ترافیک نامحدود · ۳۰ روزه", "Unlimited traffic · 30 days", "time", 30, 0, 420000, sort=5)
plan("p300", "۳۰۰ گیگ · ۹۰ روزه", "300 GB · 90 days", "both", 90, 300, 690000, sort=6)
plan("top10", "بسته ۱۰ گیگ ترافیک", "10 GB traffic package", "traffic", None, 10, 25000, topup=True, sort=7)
paid_plans = ["p10", "p30", "p100", "punl", "p300"]
plan_w = [25, 35, 15, 15, 10]

# --- discount code -----------------------------------------------------------------------------
ins("core.discount_codes", code="WELCOME20", percent=20, max_uses=100, used_count=7, enabled=True)

# --- users -------------------------------------------------------------------------------------
names = ("ali sara reza maryam hossein zahra amir narges mehdi fatemeh kian neda arash shirin omid leila pouya mina "
         "saeed parisa babak roya farid azadeh nima elham sina tina kaveh donya milad atena payam ghazal behnam "
         "sepideh shahab yasaman ramin negar hamed").split()
users = []
# created: three within 24 h, six more within the week, the rest back to 44 days
user_ages = [3 * 3600, 9 * 3600, 20 * 3600] + [int(rng.uniform(1.5, 6.5) * DAY) for _ in range(6)] + \
            [int(rng.uniform(8, 44) * DAY) for _ in range(len(names) - 9)]
for i, n in enumerate(names):
    users.append(dict(id=uid(), tg=9000100001 + i, name=f"demo_{n}", lang="fa" if rng.random() < 0.72 else "en",
                      age=user_ages[i], balance=0, events=[]))
reseller = dict(id=uid(), tg=9000100100, name="demo_reseller", lang="fa", age=40 * DAY, balance=0, events=[])
reseller2 = dict(id=fixed("reseller2"), tg=9000100101, name="demo_reseller2", lang="en", age=21 * DAY, balance=0, events=[])
resellers = [reseller, reseller2]
for u in users + resellers:
    ins("core.users", id=u["id"], telegram_id=u["tg"], username=u["name"], language=u["lang"],
        role="reseller" if any(u is r for r in resellers) else "user", status="active",
        created_at=Raw(ago(u["age"])), updated_at=Raw(ago(u["age"])))

# --- paid orders, subscriptions, payments, ledger ----------------------------------------------
# who buys: all but ten users (they get proofs waiting for review, abandoned orders or tickets); the three
# newest users buy within hours of joining
nonbuyers = [u for u in users if u["name"] in ("demo_sepideh", "demo_shahab", "demo_yasaman", "demo_ramin", "demo_negar",
                                                "demo_hamed", "demo_ghazal", "demo_behnam", "demo_payam", "demo_atena")]
buyers = [u for u in users if u not in nonbuyers]

new_orders = []
for i in range(33):
    u = buyers[i % len(buyers)]
    if i < 3:  # the three newest users buy within hours of joining
        u = sorted(buyers, key=lambda x: x["age"])[i]
        t_ago = max(600, u["age"] - rng.uniform(1800, 5400))
    else:
        t_ago = rng.uniform(0.3 * DAY, min(u["age"] - 3600, 44 * DAY)) if u["age"] > 0.4 * DAY + 3600 else max(900, u["age"] - 3000)
    pk = rng.choices(paid_plans, plan_w)[0]
    new_orders.append(dict(user=u, t_ago=t_ago, plan=pk))
# two orders today beyond the new users', so that "revenue today" is a few figures
for k in range(2):
    u = rng.choice([b for b in buyers if b["age"] > 5 * DAY])
    new_orders.append(dict(user=u, t_ago=rng.uniform(1.5, 14) * 3600, plan=rng.choice(["p30", "p100", "p10"])))
new_orders.sort(key=lambda o: -o["t_ago"])  # oldest first

method_w = [("manual_card", 50), ("manual_crypto", 12), ("stars", 10), ("wallet", 28)]
discounted = 0
subs = []
for o in new_orders:
    p = plans[o["plan"]]
    o["id"], o["sub"] = uid(), uid()
    price = p["price"]
    code, disc = None, 0
    if discounted < 7 and o["t_ago"] > 2 * DAY and rng.random() < 0.35:
        code, disc, discounted = "WELCOME20", price // 5, discounted + 1
    o["amount"], o["code"], o["disc"] = price - disc, code, disc
    o["method"] = rng.choices([m for m, _ in method_w], [w for _, w in method_w])[0]
    o["u"] = o["user"]
    o["u"]["events"].append(o)
# a handful of renewals of old services
renews = []
for o in new_orders:
    if 28 * DAY < o["t_ago"] < 44 * DAY and len(renews) < 4 and o["plan"] in ("p10", "p30", "p100"):
        renews.append(o)
renew_orders = []
for o in renews:
    ro = dict(user=o["u"], t_ago=o["t_ago"] - 27 * DAY, plan=o["plan"], id=uid(), sub=o["sub"], renew_of=o,
              amount=plans[o["plan"]]["price"], code=None, disc=0, method=rng.choice(["manual_card", "wallet"]))
    renew_orders.append(ro)
    o["u"]["events"].append(ro)

for u in users + resellers:
    u["events"].sort(key=lambda e: -e["t_ago"])

ledger = []  # (user, t_ago, kind, amount, ref_type, ref_id)
intents = []
for u in users:
    bal = 0
    for e in u["events"]:
        price = e["amount"]
        t = e["t_ago"]
        if e["method"] != "wallet":
            iid = uid()
            intents.append(dict(id=iid, order=e["id"], user=u, provider=e["method"], amount=price, t=t - 300, status="succeeded"))
            ledger.append((u, t - 200, "topup", price, "payment", iid))
        else:
            if bal < price:
                top = ((price - bal + 49999) // 50000) * 50000 + rng.choice([0, 0, 50000, 100000])
                iid = uid()
                intents.append(dict(id=iid, order=None, user=u, provider="manual_card", amount=top, t=t - 3600, status="succeeded"))
                ledger.append((u, t - 3500, "topup", top, "payment", iid))
                bal += top
            intents.append(dict(id=uid(), order=e["id"], user=u, provider="wallet", amount=price, t=t - 5, status="succeeded"))
            bal -= price
        ledger.append((u, t, "purchase", -price, "order", e["id"]))
    u["balance"] = bal

# a few users hold a balance after a top-up, for a believable ledger
for u in rng.sample([b for b in buyers if b["age"] > 6 * DAY], 8):
    top = rng.choice([50000, 100000, 150000, 200000])
    iid = uid()
    t = rng.uniform(0.2, 5) * DAY
    intents.append(dict(id=iid, order=None, user=u, provider=rng.choice(["manual_card", "manual_card", "manual_crypto"]), amount=top, t=t, status="succeeded"))
    ledger.append((u, t - 60, "topup", top, "payment", iid))
    u["balance"] += top

# the resellers buy for their customers on credit
reseller_orders = []
for (who, pk, labelname, t_ago) in [(reseller, "p30", "Customer A", 9 * DAY), (reseller, "p10", "Customer B", 6 * DAY), (reseller, "p100", "Customer C", 2 * DAY),
                                    (reseller2, "p10", "Customer D", 5 * DAY), (reseller2, "p30", "Customer E", 1 * DAY)]:
    price = plans[pk]["price"] * 90 // 100
    ro = dict(user=who, t_ago=t_ago, plan=pk, id=fixed(labelname) if who is reseller2 else uid(), sub=fixed(labelname + "/sub") if who is reseller2 else uid(),
              amount=price, code=None, disc=0, method="wallet", label=labelname)
    reseller_orders.append(ro)
    ledger.append((who, t_ago, "purchase", -price, "order", ro["id"]))
    who["balance"] -= price
settle = 150000
ledger.append((reseller, 3 * DAY, "settlement", settle, "payment", None))
reseller["balance"] += settle

# ledger rows with running balance per user
by_user = {}
for row in ledger:
    by_user.setdefault(row[0]["id"], []).append(row)
for uid_, rows in by_user.items():
    rows.sort(key=lambda r: -r[1])
    run = 0
    for (u, t, kind, amount, rt, rid) in rows:
        run += amount
        ins("core.ledger_entries", id=uid(), user_id=u["id"], currency="IRT", amount=amount, kind=kind, ref_type=rt, ref_id=rid,
            idempotency_key=f"demo:{uuid.uuid4()}", balance_after=run, created_at=Raw(ago(t)))

for u in users + resellers:
    bal = u["balance"]
    ins("core.wallets", user_id=u["id"], currency="IRT", balance=bal, updated_at=Raw(ago(60)),
        credit_limit=(500000 if any(u is r for r in resellers) else 0))

# orders and their subscriptions
sub_rows = {}
running = []  # (subscription id, client e-mail) of the services that run: the provisioner counts them per server
allnew = new_orders + reseller_orders
for o in allnew:
    p = plans[o["plan"]]
    ins("core.orders", id=o["id"], user_id=o["user"]["id"], plan_id=p["id"], type="new", status="active", amount=o["amount"],
        currency="IRT", idempotency_key=f"demo:{o['id']}", created_at=Raw(ago(o["t_ago"])), updated_at=Raw(ago(o["t_ago"] - 90)),
        discount_code=o["code"], discount_amount=o["disc"], label=o.get("label"))
    sub_rows[o["sub"]] = dict(order=o, expires_ago=o["t_ago"] - p["days"] * DAY)
for ro in renew_orders:
    sub_rows[ro["sub"]]["expires_ago"] -= plans[ro["plan"]]["days"] * DAY
# a few services that end within three days, and a few that ended in the last days (own generator: the data above stays as it was)
spread = random.Random(77)
candidates = sorted(sid for sid, row in sub_rows.items() if row["order"]["user"] is not reseller and row["order"]["user"] is not reseller2 and -row["expires_ago"] > 6 * DAY)
picked = spread.sample(candidates, 7)
for sid in picked[:4]:
    sub_rows[sid]["expires_ago"] = -spread.uniform(0.4, 2.6) * DAY
for sid in picked[4:]:
    sub_rows[sid]["expires_ago"] = spread.uniform(1, 12) * DAY
# subscription rows (renew orders point at them: set after the subscriptions exist)
for sid, s in sub_rows.items():
    o = s["order"]
    p = plans[o["plan"]]
    left = -s["expires_ago"]  # seconds until expiry (negative: expired)
    if left < 0:
        status = "expired"
    elif left < 3 * DAY:
        status = "expiring_soon"
    else:
        status = "active"
    total = p["bytes"]
    if total:
        frac = rng.uniform(0.8, 1.0) if status == "expired" else rng.uniform(0.05, 0.9)
        used = int(total * frac)
    else:
        used = int(rng.uniform(20, 160) * GB)
    if status != "expired":
        running.append((sid, f"demo-{sid[:8]}"))
    ins("core.subscriptions", id=sid, user_id=o["user"]["id"], order_id=o["id"], server_id=None,
        client_email=f"demo-{sid[:8]}", sub_id=sid.replace("-", "")[:16], status=status,
        expires_at=Raw(ago(s["expires_ago"])), traffic_total_bytes=total, traffic_used_bytes=used,
        last_synced_at=Raw("now()"), created_at=Raw(ago(o["t_ago"] - 60)), updated_at=Raw(ago(60)),
        sub_link=f"https://sub.example.invalid/s/{sid.replace('-', '')[:16]}",
        notified_expiring_at=Raw("now()"), notified_low_traffic_at=Raw("now()"), notified_ended_at=Raw("now()"),
        label=o.get("label"))
# renewals come after the subscriptions they extend
for ro in renew_orders:
    p = plans[ro["plan"]]
    ins("core.orders", id=ro["id"], user_id=ro["user"]["id"], plan_id=p["id"], type="renew", subscription_id=ro["sub"],
        status="active", amount=ro["amount"], currency="IRT", idempotency_key=f"demo:{ro['id']}",
        created_at=Raw(ago(ro["t_ago"])), updated_at=Raw(ago(ro["t_ago"] - 90)))

for it in intents:
    ins("payments.payment_intents", id=it["id"], order_id=it["order"], user_id=it["user"]["id"], provider=it["provider"],
        amount=it["amount"], currency="IRT", status=it["status"], idempotency_key=f"demo:{it['id']}",
        created_at=Raw(ago(it["t"])), updated_at=Raw(ago(it["t"] - 60)))

# --- waiting for review: five proofs, one pair with the same bank reference ------------------------
free = [u for u in nonbuyers]
pending = [
    # (user, plan, provider, order?, amount, minutes ago, reference, txid, receipt photo)
    (free[0], "p30", "manual_card", True, None, 8, "814420917236", None, True),
    (free[1], "p10", "manual_card", True, None, 25, "602913385104", None, True),
    (free[2], None, "manual_card", False, 200000, 47, "519384720655", None, True),
    (free[3], "p100", "manual_crypto", True, None, 130, None, "b3f1c0a97d22e65f4a1d8e9c7b5a3f20d6c8e1ab94f7305d2c6e8a1b7f49d03e", False),
    (free[4], "p30", "manual_card", True, None, 190, "814420917236", None, True),
]
for (u, pk, prov, has_order, amount, mins, ref, txid, photo) in pending:
    iid = uid()
    oid = uid() if has_order else None
    amt = amount if amount else plans[pk]["price"]
    if has_order:
        ins("core.orders", id=oid, user_id=u["id"], plan_id=plans[pk]["id"], type="new", status="awaiting_payment", amount=amt,
            currency="IRT", idempotency_key=f"demo:{oid}", created_at=Raw(ago(mins * 60 + 400)), updated_at=Raw(ago(mins * 60)))
    ins("payments.payment_intents", id=iid, order_id=oid, user_id=u["id"], provider=prov, amount=amt, currency="IRT",
        status="confirming", idempotency_key=f"demo:{iid}", expires_at=Raw("now() + interval '20 hours'"),
        created_at=Raw(ago(mins * 60 + 300)), updated_at=Raw(ago(mins * 60)))
    ins("payments.manual_receipts", id=uid(), intent_id=iid, network=("TRC20" if txid else None), txid=txid,
        receipt_file=("demo-receipt" if photo else None), reference_number=ref, submitted_at=Raw(ago(mins * 60)))
# started but not paid, and abandoned
for (u, pk, st, ost, mins) in [(free[5], "p10", "pending", "awaiting_payment", 70), (free[6], "p30", "pending", "awaiting_payment", 260),
                               (free[7], "p100", "expired", "expired", 60 * 30), (free[8], "p10", "failed", "cancelled", 60 * 50)]:
    oid, iid = uid(), uid()
    ins("core.orders", id=oid, user_id=u["id"], plan_id=plans[pk]["id"], type="new", status=ost, amount=plans[pk]["price"],
        currency="IRT", idempotency_key=f"demo:{oid}", created_at=Raw(ago(mins * 60)), updated_at=Raw(ago(mins * 60)))
    ins("payments.payment_intents", id=iid, order_id=oid, user_id=u["id"], provider="manual_card", amount=plans[pk]["price"], currency="IRT",
        status=st, idempotency_key=f"demo:{iid}", expires_at=Raw(ago(-3 * 3600 if st == "pending" else mins * 60 - 3600)),
        created_at=Raw(ago(mins * 60)), updated_at=Raw(ago(mins * 60)))

# --- support tickets ------------------------------------------------------------------------------
tick = [
    (nonbuyers[0], "payment", "open", 8 * 60 + 30, [
        ("customer", "سلام، پرداخت کارت‌به‌کارت انجام شد و رسید را فرستادم. لطفاً بررسی کنید.", 8 * 60)]),
    (buyers[3], "service", "open", 95 * 60, [
        ("customer", "Hi, my link connects on Wi-Fi but not on mobile data. What should I check?", 95 * 60)]),
    (buyers[7], "other", "answered", 5 * 3600, [
        ("customer", "آیا این سرویس با Hiddify هم کار می‌کند؟", 6 * 3600),
        ("staff", "سلام، بله. لینک اشتراک را در Hiddify اضافه کنید؛ برای راهنمای کامل از منوی پشتیبانی کمک بگیرید.", 5 * 3600)]),
    (buyers[11], "service", "closed", 2 * DAY, [
        ("customer", "My traffic bar looks wrong after renewing.", 2 * DAY + 3600),
        ("staff", "Thanks for the report: the bar counts the days and traffic you added. Both are added together, so it is correct now.", 2 * DAY + 1800),
        ("customer", "Got it, thanks!", 2 * DAY)]),
]
for (u, cat, st, upd, msgs) in tick:
    tid = uid()
    first = msgs[0][1]
    ins("core.tickets", id=tid, user_id=u["id"], category=cat, subject=first[:80], status=st,
        created_at=Raw(ago(msgs[0][2])), updated_at=Raw(ago(upd)),
        last_customer_at=Raw(ago([m[2] for m in msgs if m[0] == "customer"][-1])),
        last_staff_at=(Raw(ago([m[2] for m in msgs if m[0] == "staff"][-1])) if any(m[0] == "staff" for m in msgs) else None),
        closed_at=(Raw(ago(upd)) if st == "closed" else None), closed_by=(u["id"] if st == "closed" else None),
        close_reason=("customer" if st == "closed" else None))
    for (kind, body, t) in msgs:
        ins("core.ticket_messages", id=uid(), ticket_id=tid, kind=kind, author_id=(u["id"] if kind == "customer" else OWNER),
            body=body, delivery=("sent" if kind == "staff" else None), delivered_at=(Raw(ago(t)) if kind == "staff" else None),
            created_at=Raw(ago(t)))

# --- reseller --------------------------------------------------------------------------------------
tier = uid()
ins("core.reseller_tiers", id=tier, name="Silver", discount_percent=10, commission_percent=5)
for r in resellers:
    ins("core.resellers", user_id=r["id"], tier_id=tier, commission_percent=5, note="demo")

# --- payment details, so the methods read "on" -------------------------------------------------------
for k, v in [("payments.card_number", "0000 0000 0000 0000"), ("payments.card_holder", "Demo Store"),
             ("payments.usdt_trc20", "T" + "X" * 33), ("payments.stars_rate", "1500"),
             ("payments.manual_zarinpal.enabled", "false")]:
    out.append(f"INSERT INTO core.settings (key, value) VALUES ({q(k)}, {q(json.dumps(v))}::jsonb) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;")

# --- two servers, with the running services spread over them -----------------------------------------
# The second server is a copy of the lab's (same panel, same token), so it answers its health checks too.
server2 = fixed("server2")
out.append("INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, panel_version, enabled, sub_base_url, allow_private, weight, managed,"
           " last_health_at, last_health_ok, last_error, last_checked_at) "
           f"SELECT {q(server2)}, name || '-2', base_url, api_token_enc, panel_version, enabled, sub_base_url, allow_private, weight, managed,"
           f" now(), true, '', now() FROM provisioner.xui_servers WHERE id = {q(SERVER)};")
# the lab's own client already has a row there, so one fewer: the card then counts exactly the services that run
for i, (sid, email) in enumerate(running[:-1]):
    ins("provisioner.client_map", subscription_id=sid, server_id=(SERVER if i % 2 == 0 else server2), email=email,
        xui_sub_id=sid.replace("-", "")[:16], inbound_ids=Raw("ARRAY[]::integer[]"), pending=False)

print("BEGIN;")
print("\n".join(out))
print("COMMIT;")
