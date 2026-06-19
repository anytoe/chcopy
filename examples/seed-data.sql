INSERT INTO shop.users (id, name, email, created_at) VALUES
    (1, 'alice', 'alice@example.com', '2026-04-15 10:00:00'),
    (2, 'bob',   'bob@example.com',   '2026-04-20 11:00:00'),
    (3, 'cara',  'cara@example.com',  '2026-05-02 12:00:00'),
    (4, 'dave',  'dave@example.com',  '2026-05-10 13:00:00');

INSERT INTO shop.orders (id, user_id, amount, created_at) VALUES
    (1, 1, 19.99,  '2026-04-15 10:30:00'),
    (2, 1, 29.50,  '2026-04-21 09:15:00'),
    (3, 2, 199.00, '2026-04-25 14:00:00'),
    (4, 3, 42.00,  '2026-05-03 16:45:00'),
    (5, 3, 75.25,  '2026-05-08 18:00:00'),
    (6, 4, 12.00,  '2026-05-12 20:00:00');

INSERT INTO shop.events (id, year, name) VALUES
    (1, 2023, 'old launch'),
    (2, 2024, 'spring sale'),
    (3, 2024, 'summer sale'),
    (4, 2025, 'winter sale'),
    (5, 2025, 'black friday'),
    (6, 2025, 'cyber monday');
