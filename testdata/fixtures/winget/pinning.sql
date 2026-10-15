-- winget's pinning.db of the fake machine: Northwind Player is pinned.
CREATE TABLE [metadata]([name] TEXT PRIMARY KEY NOT NULL, [value] TEXT NOT NULL);
CREATE TABLE [pin]([package_id] TEXT NOT NULL, [source_id] TEXT NOT NULL, [type] INT64 NOT NULL, [version] TEXT NOT NULL);
CREATE UNIQUE INDEX [pin_index] ON [pin]([package_id], [source_id]);
INSERT INTO metadata VALUES ('majorVersion', '1'), ('minorVersion', '0');
INSERT INTO pin VALUES ('Northwind.Player', 'Microsoft.Winget.Source_8wekyb3d8bbwe', 2, '');
