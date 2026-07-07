-- +goose Up
-- Идемпотентный сид (ADR-007): реальные регионы и города Узбекистана.
-- Координаты — приблизительный центр населённого пункта (WGS84).

INSERT INTO regions (id, slug, name_uz, name_ru, latitude, longitude, sort_order) VALUES
 (1,  'toshkent-shahri', 'Toshkent shahri',                'город Ташкент',             41.3111, 69.2797, 1),
 (2,  'toshkent',        'Toshkent viloyati',              'Ташкентская область',       41.2000, 69.5000, 2),
 (3,  'andijon',         'Andijon viloyati',               'Андижанская область',       40.7833, 72.3500, 3),
 (4,  'buxoro',          'Buxoro viloyati',                'Бухарская область',         39.7680, 64.4210, 4),
 (5,  'fargona',         'Farg''ona viloyati',             'Ферганская область',        40.3864, 71.7864, 5),
 (6,  'jizzax',          'Jizzax viloyati',                'Джизакская область',        40.1158, 67.8422, 6),
 (7,  'xorazm',          'Xorazm viloyati',                'Хорезмская область',        41.5500, 60.6333, 7),
 (8,  'namangan',        'Namangan viloyati',              'Наманганская область',      40.9983, 71.6726, 8),
 (9,  'navoiy',          'Navoiy viloyati',                'Навоийская область',        40.0844, 65.3792, 9),
 (10, 'qashqadaryo',     'Qashqadaryo viloyati',           'Кашкадарьинская область',   38.8600, 65.7900, 10),
 (11, 'samarqand',       'Samarqand viloyati',             'Самаркандская область',     39.6542, 66.9597, 11),
 (12, 'sirdaryo',        'Sirdaryo viloyati',              'Сырдарьинская область',     40.3833, 68.6667, 12),
 (13, 'surxondaryo',     'Surxondaryo viloyati',           'Сурхандарьинская область',  37.9409, 67.5700, 13),
 (14, 'qoraqalpogiston', 'Qoraqalpog''iston Respublikasi', 'Республика Каракалпакстан', 42.4600, 59.6100, 14)
ON CONFLICT (id) DO UPDATE SET
  slug = EXCLUDED.slug, name_uz = EXCLUDED.name_uz, name_ru = EXCLUDED.name_ru,
  latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude, sort_order = EXCLUDED.sort_order;

INSERT INTO cities (id, region_id, slug, name_uz, name_ru, latitude, longitude, sort_order) VALUES
 -- Toshkent shahri (районы)
 (101, 1, 'bektemir',      'Bektemir',        'Бектемир',      41.2100, 69.3340, 1),
 (102, 1, 'chilonzor',     'Chilonzor',       'Чиланзар',      41.2758, 69.2036, 2),
 (103, 1, 'mirobod',       'Mirobod',         'Мирабад',       41.2900, 69.2900, 3),
 (104, 1, 'mirzo-ulugbek', 'Mirzo Ulug''bek', 'Мирзо-Улугбек', 41.3300, 69.3400, 4),
 (105, 1, 'olmazor',       'Olmazor',         'Алмазар',       41.3450, 69.2050, 5),
 (106, 1, 'sergeli',       'Sergeli',         'Сергели',       41.2230, 69.2220, 6),
 (107, 1, 'shayxontohur',  'Shayxontohur',    'Шайхантахур',   41.3260, 69.2280, 7),
 (108, 1, 'uchtepa',       'Uchtepa',         'Учтепа',        41.2870, 69.1810, 8),
 (109, 1, 'yakkasaroy',    'Yakkasaroy',      'Яккасарай',     41.2870, 69.2660, 9),
 (110, 1, 'yashnobod',     'Yashnobod',       'Яшнабад',       41.2900, 69.3200, 10),
 (111, 1, 'yunusobod',     'Yunusobod',       'Юнусабад',      41.3670, 69.2890, 11),
 (112, 1, 'yangihayot',    'Yangihayot',      'Янгихаёт',      41.2200, 69.2000, 12),
 -- Toshkent viloyati
 (201, 2, 'nurafshon', 'Nurafshon',  'Нурафшан',  41.0167, 69.3500, 1),
 (202, 2, 'chirchiq',  'Chirchiq',   'Чирчик',    41.4689, 69.5822, 2),
 (203, 2, 'angren',    'Angren',     'Ангрен',    41.0167, 70.1436, 3),
 (204, 2, 'bekobod',   'Bekobod',    'Бекабад',   40.2206, 69.2694, 4),
 (205, 2, 'olmaliq',   'Olmaliq',    'Алмалык',   40.8442, 69.5983, 5),
 (206, 2, 'yangiyol',  'Yangiyo''l', 'Янгиюль',   41.1128, 69.0478, 6),
 (207, 2, 'ohangaron', 'Ohangaron',  'Ахангаран', 40.9061, 69.6383, 7),
 -- Andijon
 (301, 3, 'andijon',   'Andijon',   'Андижан',  40.7833, 72.3333, 1),
 (302, 3, 'asaka',     'Asaka',     'Асака',    40.6414, 72.2408, 2),
 (303, 3, 'xonobod',   'Xonobod',   'Ханабад',  40.8069, 72.9631, 3),
 (304, 3, 'shahrixon', 'Shahrixon', 'Шахрихан', 40.7147, 72.0561, 4),
 -- Buxoro
 (401, 4, 'buxoro',   'Buxoro',     'Бухара',   39.7680, 64.4210, 1),
 (402, 4, 'kogon',    'Kogon',      'Каган',    39.7222, 64.5528, 2),
 (403, 4, 'gijduvon', 'G''ijduvon', 'Гиждуван', 40.1017, 64.6822, 3),
 -- Farg'ona
 (501, 5, 'fargona',  'Farg''ona',  'Фергана',  40.3864, 71.7864, 1),
 (502, 5, 'qoqon',    'Qo''qon',    'Коканд',   40.5286, 70.9425, 2),
 (503, 5, 'margilon', 'Marg''ilon', 'Маргилан', 40.4711, 71.7247, 3),
 (504, 5, 'quvasoy',  'Quvasoy',    'Кувасай',  40.2967, 71.9744, 4),
 -- Jizzax
 (601, 6, 'jizzax',  'Jizzax',  'Джизак',  40.1158, 67.8422, 1),
 (602, 6, 'gagarin', 'Gagarin', 'Гагарин', 40.3667, 67.9333, 2),
 -- Xorazm
 (701, 7, 'urganch', 'Urganch', 'Ургенч', 41.5500, 60.6333, 1),
 (702, 7, 'xiva',    'Xiva',    'Хива',   41.3783, 60.3639, 2),
 -- Namangan
 (801, 8, 'namangan', 'Namangan', 'Наманган', 40.9983, 71.6726, 1),
 (802, 8, 'chust',    'Chust',    'Чуст',     41.0000, 71.2333, 2),
 (803, 8, 'kosonsoy', 'Kosonsoy', 'Касансай', 41.2494, 71.5497, 3),
 -- Navoiy
 (901, 9, 'navoiy',    'Navoiy',    'Навои',    40.0844, 65.3792, 1),
 (902, 9, 'zarafshon', 'Zarafshon', 'Зарафшан', 41.5744, 64.2036, 2),
 (903, 9, 'uchquduq',  'Uchquduq',  'Учкудук',  42.1553, 63.5561, 3),
 -- Qashqadaryo
 (1001, 10, 'qarshi',     'Qarshi',     'Карши',     38.8600, 65.7900, 1),
 (1002, 10, 'shahrisabz', 'Shahrisabz', 'Шахрисабз', 39.0578, 66.8300, 2),
 (1003, 10, 'kitob',      'Kitob',      'Китаб',     39.1206, 66.8800, 3),
 -- Samarqand
 (1101, 11, 'samarqand',   'Samarqand',       'Самарканд',   39.6542, 66.9597, 1),
 (1102, 11, 'kattaqorgon', 'Kattaqo''rg''on', 'Каттакурган', 39.8994, 66.2497, 2),
 (1103, 11, 'urgut',       'Urgut',           'Ургут',       39.4028, 67.2417, 3),
 -- Sirdaryo
 (1201, 12, 'guliston', 'Guliston', 'Гулистан', 40.4897, 68.7842, 1),
 (1202, 12, 'yangiyer', 'Yangiyer', 'Янгиер',   40.2603, 68.8250, 2),
 (1203, 12, 'shirin',   'Shirin',   'Ширин',    40.2333, 69.0833, 3),
 -- Surxondaryo
 (1301, 13, 'termiz',  'Termiz',    'Термез', 37.2242, 67.2783, 1),
 (1302, 13, 'denov',   'Denov',     'Денау',  38.2667, 67.9000, 2),
 (1303, 13, 'shorchi', 'Sho''rchi', 'Шурчи',  37.9917, 67.7889, 3),
 -- Qoraqalpog'iston
 (1401, 14, 'nukus',   'Nukus',     'Нукус',    42.4600, 59.6100, 1),
 (1402, 14, 'moynoq',  'Mo''ynoq',  'Муйнак',   43.7686, 59.0217, 2),
 (1403, 14, 'xojayli', 'Xo''jayli', 'Ходжейли', 42.4053, 59.4506, 3),
 (1404, 14, 'beruniy', 'Beruniy',   'Беруни',   41.6906, 60.7522, 4)
ON CONFLICT (id) DO UPDATE SET
  region_id = EXCLUDED.region_id, slug = EXCLUDED.slug,
  name_uz = EXCLUDED.name_uz, name_ru = EXCLUDED.name_ru,
  latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude, sort_order = EXCLUDED.sort_order;

-- +goose Down
DELETE FROM cities;
DELETE FROM regions;
