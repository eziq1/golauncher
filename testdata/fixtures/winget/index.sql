-- The index of a fake "winget" source: the schema 2.0 tables of the real
-- Public\index.db, with invented packages.
CREATE TABLE [metadata]([name] TEXT PRIMARY KEY NOT NULL, [value] TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE [packages](rowid INTEGER PRIMARY KEY, [id] TEXT NOT NULL, [name] TEXT NOT NULL, [moniker] TEXT, [latest_version] TEXT NOT NULL, [arp_min_version] TEXT, [arp_max_version] TEXT, [hash] BLOB);
CREATE TABLE [tags2](rowid INTEGER PRIMARY KEY, [tag] TEXT NOT NULL);
CREATE TABLE [tags2_map]([tag] INT64 NOT NULL, [package] INT64 NOT NULL, PRIMARY KEY([tag], [package])) WITHOUT ROWID;
CREATE TABLE [commands2](rowid INTEGER PRIMARY KEY, [command] TEXT NOT NULL);
CREATE TABLE [commands2_map]([command] INT64 NOT NULL, [package] INT64 NOT NULL, PRIMARY KEY([command], [package])) WITHOUT ROWID;
CREATE TABLE [pfns2]([pfn] TEXT NOT NULL, [package] INT64 NOT NULL, PRIMARY KEY([pfn], [package])) WITHOUT ROWID;
CREATE TABLE [productcodes2]([productcode] TEXT NOT NULL, [package] INT64 NOT NULL, PRIMARY KEY([productcode], [package])) WITHOUT ROWID;
CREATE TABLE [norm_names2]([norm_name] TEXT NOT NULL, [package] INT64 NOT NULL, PRIMARY KEY([norm_name], [package])) WITHOUT ROWID;
CREATE TABLE [norm_publishers2]([norm_publisher] TEXT NOT NULL, [package] INT64 NOT NULL, PRIMARY KEY([norm_publisher], [package])) WITHOUT ROWID;
CREATE TABLE [upgradecodes2]([upgradecode] TEXT NOT NULL, [package] INT64 NOT NULL, PRIMARY KEY([upgradecode], [package])) WITHOUT ROWID;

INSERT INTO metadata VALUES ('majorVersion', '2'), ('minorVersion', '0'), ('lastwritetime', '1790000000');

INSERT INTO packages (rowid, id, name, moniker, latest_version) VALUES
  (1, 'Contoso.TextPad', 'Contoso TextPad', 'textpad', '1.4.1'),
  (2, 'Contoso.TextPad.Preview', 'Contoso TextPad Preview', NULL, '1.5.0-beta.2'),
  (3, 'Fabrikam.Gitty', 'Gitty', 'gitty', '2.0.0'),
  (4, 'Fabrikam.GitGui', 'Fabrikam Git GUI', NULL, '3.2.1'),
  (5, 'Northwind.Player', 'Northwind Player', 'nwplayer', '3.2.0'),
  (6, 'Tailspin.Toolbox', 'Tailspin Toolbox', NULL, '0.9.4'),
  (7, 'Adatum.Chatty', '微聊', 'chatty', '5.1.0'),
  (8, 'Woodgrove.Notes', 'Woodgrove Notes', NULL, '7.0.0'),
  (9, 'Litware.Zipper', 'Litware Zipper', 'lzip', '24.08');

INSERT INTO tags2 (rowid, tag) VALUES
  (1, 'editor'), (2, 'text'), (3, 'git'), (4, 'vcs'), (5, 'gui'), (6, 'media'),
  (7, 'music'), (8, 'cli'), (9, 'chat'), (10, '聊天'), (11, 'notes'), (12, 'markdown'), (13, 'archive');
INSERT INTO tags2_map (tag, package) VALUES
  (1, 1), (2, 1), (1, 2), (3, 3), (4, 3), (3, 4), (5, 4), (6, 5), (7, 5),
  (8, 6), (9, 7), (10, 7), (11, 8), (12, 8), (13, 9);

INSERT INTO commands2 (rowid, command) VALUES (1, 'textpad'), (2, 'gitty'), (3, 'nwplay'), (4, 'tt'), (5, 'lz');
INSERT INTO commands2_map (command, package) VALUES (1, 1), (2, 3), (3, 5), (4, 6), (5, 9);
