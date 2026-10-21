import { expect, test } from "bun:test";
import { normalizeBookmarkTree } from "./bookmark_tree.js";

test("firefox tree: roots, separators, nesting", () => {
  const tree = [
    {
      id: "root________",
      type: "folder",
      title: "",
      children: [
        {
          id: "menu________",
          parentId: "root________",
          type: "folder",
          title: "Bookmarks Menu",
          index: 0,
          children: [],
        },
        {
          id: "toolbar_____",
          parentId: "root________",
          type: "folder",
          title: "Bookmarks Toolbar",
          index: 1,
          children: [
            {
              id: "f1",
              parentId: "toolbar_____",
              type: "folder",
              title: "sub",
              index: 0,
              dateAdded: 1700000000000,
              children: [
                {
                  id: "b1",
                  parentId: "f1",
                  type: "bookmark",
                  title: "a",
                  url: "https://example.com/a",
                  index: 0,
                },
                {
                  id: "s1",
                  parentId: "f1",
                  type: "separator",
                  title: "",
                  url: "data:",
                  index: 1,
                },
              ],
            },
          ],
        },
      ],
    },
  ];

  const roots = normalizeBookmarkTree(tree);
  expect(roots.map((r) => r.root)).toEqual(["menu", "toolbar"]);
  expect(roots.every((r) => r.unmodifiable)).toBe(true);

  const sub = roots[1].children[0];
  expect(sub.kind).toBe("folder");
  expect(sub.unmodifiable).toBe(false);
  expect(sub.children[0]).toMatchObject({
    kind: "bookmark",
    url: "https://example.com/a",
  });
  expect(sub.children[1].kind).toBe("separator");
  expect(sub.children[1].url).toBeUndefined();
});

test("chrome tree: folderType roots, kind from url presence", () => {
  const tree = [
    {
      id: "0",
      title: "",
      children: [
        {
          id: "1",
          parentId: "0",
          folderType: "bookmarks-bar",
          title: "Bookmarks bar",
          children: [
            {
              id: "5",
              parentId: "1",
              index: 0,
              title: "a",
              url: "https://example.com/",
            },
            {
              id: "6",
              parentId: "1",
              index: 1,
              title: "managed",
              unmodifiable: "managed",
              children: [],
            },
          ],
        },
        {
          id: "2",
          parentId: "0",
          folderType: "other",
          title: "Other",
          children: [],
        },
      ],
    },
  ];

  const roots = normalizeBookmarkTree(tree);
  expect(roots.map((r) => r.root)).toEqual(["bar", "other"]);
  expect(roots[0].children[0]).toMatchObject({
    kind: "bookmark",
    url: "https://example.com/",
  });
  expect(roots[0].children[1]).toMatchObject({
    kind: "folder",
    unmodifiable: true,
  });
});

test("empty and missing trees", () => {
  expect(normalizeBookmarkTree([])).toEqual([]);
  expect(normalizeBookmarkTree(undefined)).toEqual([]);
});
