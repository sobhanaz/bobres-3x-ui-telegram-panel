# Reselling a supplier's digital accounts (the owner's own store)

The owner's own BOBRES store can resell the accounts of a supplier (the first one: the reseller API at
`https://ssondigitalworks.online/api/reseller`). It is **off on every install unless the owner sets it up**: a customer's
bot, dashboard and wallet are unchanged.

## How it works

- The supplier's API is `GET ?action=products`, `GET ?action=balance` and `POST ?action=order` with
  `{product_id, quantity, external_order_id}` and `Authorization: Bearer <API key>`. An order's `external_order_id` is
  unique: asking again with the same id returns the same order and charges nothing again. BOBRES uses the order's own id.
- **Core has no internet access**, so every call goes through the bot: core brings the API key per call to the bot's
  `POST /internal/supplier`, and the bot calls the ONE supplier address it is configured for (`BOBRES_SUPPLIER_URL`, default
  the address above). Core names an action, never an address.
- **The catalog** is read every half hour (and with "Read the catalog now") into `supplier_products`. A new product comes in
  **switched off and unpriced**; the owner writes the **price in Toman** and the names and descriptions (Persian and English)
  and switches it on. A product the supplier no longer lists, or has no stock of, is not offered. The supplier's own text is
  for the owner only: customers see the owner's.
- **A purchase** is paid from the customer's wallet in Toman (the bot's ordinary top-up fills it). ONE transaction takes the
  money and writes the order; the supplier is asked outside it with the order id; the answer (the account) is stored
  encrypted with core's master key and handed to the customer in the bot, and again under "My purchases". A supplier that
  does not answer leaves the order **paid and waiting** (a worker asks again with a growing wait); one that refuses for good
  (a 4xx other than 408 and 429, or a failure in its answer) gives the money back at once. An order the supplier never
  delivers can be retried or refunded from the dashboard (refund it only when the supplier did not charge for it).
- **The balance** at the supplier is on the Digital accounts page and on the Overview (owner only), read when the page opens and
  every two minutes while it is open; core keeps a balance it read for a minute (`GET /api/v1/supplier/balance`, `?fresh=1`
  asks the supplier). It is read from a number or a text like "5.53 USDT" under any key that names a balance. When the answer
  cannot be understood the page shows it (the API key is taken out), so the reading can be taught.
- The supplier's texts are cleaned of Telegram custom-emoji placeholders (`{pe:123...}`); the raw product keeps them.
- The supplier's answers are read tolerantly (the product list may be under `products`, `data`, `items`...; the delivery under
  `delivery`, `accounts`, `credentials`...). What is not understood stays in the product's raw JSON.

## Setting it up

1. The dashboard > **Digital accounts** (owner only): paste the supplier's API key (kept encrypted, never shown again) and
   "Read the catalog now".
2. Price and switch on the products you want to sell; write the names and descriptions customers should see.
3. Turn on **the Digital accounts button** (the `supplier.enabled` setting). The bot's home menu then shows it.

## Limits

- Prices are in Toman only. Quantity in the bot is 1 (core takes up to 10).
- The supplier's stock figure is trusted as read (every half hour).
- What a delivery looks like depends on the supplier's answer: it is shown as text; check the first real order.
