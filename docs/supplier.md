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
- **The catalog** is read every half hour (and with "Read the catalog now", and by the Digital accounts page itself when it opens on a
  catalog read more than ten minutes ago) into `supplier_products`. A new product comes in **switched off**; its **price in Toman** is
  worked out from the USDT price when the owner has switched the pricing on (see "Pricing" below), else the owner writes it; the owner writes
  the names and descriptions (Persian and English) and switches it on. A product the supplier no longer lists, or has no stock of, is shown as sold out. A customer reads the owner's description (in their
  language, else the other language's), and when the owner wrote none, the supplier's own text; **Copy the supplier's name and text** in the product's
  editor starts an edit from it. The Digital accounts page can **Export texts** (every product's name and text as a JSON file) and **Import texts** (a file
  made by it comes back, with the Persian texts filled in by a translator, and only the texts it gives are changed; prices and switches are not).
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
- **In the bot** the screens follow the supplier's own: the **home** is the supplier's **categories** (the `category` of each product, two to a row, an icon
  by a word in its name, red when nothing in one is in stock; "Other" holds the products with none; with at most one category the products are listed
  directly), a **category** lists its products (green, red with "(out of stock)" when sold out, a red Back), a **product page** shows the description, the unit
  price and the stock with **Buy now**, **how many** (1, 2, 3, 5, 10, 15, 20, 25, no more than the stock, or a typed number), an **order summary** (the price,
  the total, the wallet and what is left after) and **Pay with wallet** (green; a short wallet shows how much is missing and a top-up button). The callbacks:
  `sup`, `sup:c:<key>`, `sup:p:<sid>`, `sup:q:<sid>`, `sup:n:<sid>`, `sup:s:<sid>:<qty>`, `sup:y:<sid>:<qty>:<nonce>`.
- **A product that is out of stock is still shown** (switched on and priced): its button says "Sold out", its description can be read, and it cannot be bought
  (also not with an old button). The same for a product the supplier no longer lists. While anything on the shelf is sold out the catalog is read every
  five minutes (else every half hour), so a restock is offered within minutes. The bot's **bottom menu** has the Digital accounts button (and the
  BOBRES licenses one), each while it is on.
- The supplier's texts are cleaned of Telegram custom-emoji placeholders (`{pe:123...}`); the raw product keeps them.
- The supplier's answers are read tolerantly (the product list may be under `products`, `data`, `items`...; the delivery under
  `delivery`, `accounts`, `credentials`...). What is not understood stays in the product's raw JSON.

## Pricing

The owner prices the accounts from the **live USDT price** instead of by hand. On the Digital accounts page, the pricing card shows the price
of one USDT in Toman and has the owner's **markup in percent**, a switch, and **Apply to all products**.

- **The formula.** `price = the supplier's cost (USDT) x the USDT rate x (100 + markup) / 100`, rounded **up** to the next 1 000 Toman, worked out exactly
  (integers, no floating point). The markup is 0 to 500 percent. A price over the most core takes (`1 000 000 000 000`) is not set.
- **The rate** is the one every other conversion of the store uses (`UsdtRate`, the one the bot's wallet and the license prices use): the live price
  the bot reads from the public price pages, plus the payments margin (Gateways, `payments.usdt_rate_margin`, 0 by default), else the rate the owner
  typed there while the live one is older than half an hour. The card shows which one it is and when the live price was read, and **Read the price now**
  reads it. While the pricing is on, the live price is kept fresh every two minutes.
- **Two modes per product.** *Follows USDT* (`price_mode = auto`): core sets the price. *Own price* (`fixed`): the owner's typed price, which nothing changes.
  Typing a price that is not the product's price makes it an own price; **Follow the USDT price** in the product's editor makes it follow (and prices it
  at once). A product the supplier gives no dollar cost for (no cost, or in another currency) can only have its own price. The table shows each
  product's mode, and for an own price what the formula would give, to compare.
- **The prices follow the rate in steps.** They are worked out again after every catalog read, and when the rate has moved **half a percent** from the
  one the prices were last worked out at, so a price does not flicker with every tick of the market. A catalog read inside half a percent keeps the
  prices as they are (the new products and the new costs it brings are priced at the rate of the last time). An unknown rate (none read, none typed)
  changes nothing: a product on sale never loses its price.
- **Switching it on** (the switch, with the markup, **Save and price**) prices every product that follows the rate at once and needs a known rate.
  Before that, nothing is priced and nothing changes: a store from before keeps every price it has (migration 00060 makes the products that have a price
  *own prices*, the others *follow*). **Apply to all products** makes every product that can follow the rate follow it, so the prices the owner typed are
  replaced (the confirmation says how many); the owner can type a price for any single product again afterwards.
- **A new product** is priced when it arrives (when the pricing is on) and still comes in **switched off**: the owner looks at the price and the texts
  and switches it on. A customer who was shown a price that has since moved is told "the price changed: look at it again" (the order's expected amount).
- The switch, the markup and the rate the prices were last worked out at are one internal value (`state.supplier.pricing`, not a setting). The API:
  `GET /api/v1/supplier/pricing`, `PUT /api/v1/supplier/pricing {auto, markup}`, `POST /api/v1/supplier/pricing/apply {all}` and, on a product,
  `price_mode` (`auto` or `fixed`) in `PUT /api/v1/supplier/products/{id}`. Changes are audited (`supplier.pricing`, `supplier.reprice`,
  `supplier.product`; the worker's own changes are the system's).

## Texts for new products

The page lists the products that **need texts** (no Persian name or description yet; the filter and a tag on the row). **Export texts** writes every
product to a JSON file with `needs_translation`, the supplier's cost and a short style guide for the translator (Persian in `fa`, English in the BOBRES
style in `en`, every URL, code and instruction kept as the supplier wrote it, the supplier never named). **Import texts** brings the file back.

## Setting it up

1. The dashboard > **Digital accounts** (owner only): paste the supplier's API key (kept encrypted, never shown again) and
   "Read the catalog now".
2. Set the pricing (the markup and the switch) or write a price for each product, write the names and descriptions customers should see, and switch on
   the products you want to sell.
3. Turn on **the Digital accounts button** (the `supplier.enabled` setting). The bot's home menu then shows it.

## Limits

- Prices are in Toman only (the USDT pricing needs the supplier's cost in USDT, USD or USDC). A purchase takes 1 to 25 at once (the supplier's own limit is not known; one answer carries all the accounts).
- The supplier's stock figure is trusted as read (every half hour).
- What a delivery looks like depends on the supplier's answer: it is shown as text; check the first real order.
