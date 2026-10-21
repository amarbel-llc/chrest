// Normalizes the result of browser.bookmarks.getTree() into one
// browser-independent shape (chrest#122). Pure: no browser API access, so it
// is testable outside the extension.

const FIREFOX_ROOTS = {
  toolbar_____: "toolbar",
  menu________: "menu",
  unfiled_____: "unfiled",
  mobile______: "mobile",
};

const CHROME_ROOTS = {
  "bookmarks-bar": "bar",
  other: "other",
  mobile: "mobile",
};

function rootName(node) {
  return FIREFOX_ROOTS[node.id] ?? CHROME_ROOTS[node.folderType] ?? null;
}

// Firefox reports `type` (bookmark|folder|separator); a separator carries a
// data: url, so `url` presence alone cannot classify it. Chrome has no
// `type`: nodes without a url are folders.
function kindOf(node) {
  if (node.type === "folder" || node.type === "separator") {
    return node.type;
  }

  if (node.type === "bookmark") {
    return "bookmark";
  }

  return node.url !== undefined ? "bookmark" : "folder";
}

function normalizeNode(node, isTopLevel) {
  const kind = kindOf(node);

  const out = {
    id: String(node.id),
    parentId: node.parentId === undefined ? null : String(node.parentId),
    index: node.index === undefined ? null : node.index,
    kind,
    title: node.title ?? "",
    dateAdded: node.dateAdded === undefined ? null : node.dateAdded,
    unmodifiable: isTopLevel || node.unmodifiable === "managed",
  };

  if (kind === "bookmark") {
    out.url = node.url;
  }

  if (isTopLevel) {
    out.root = rootName(node);
  }

  if (kind === "folder") {
    out.children = (node.children ?? []).map((c) => normalizeNode(c, false));
  }

  return out;
}

// getTree() returns a one-element array holding an unnamed root whose
// children are the browser's top-level folders; those become `roots`.
export function normalizeBookmarkTree(tree) {
  const top = tree?.[0]?.children ?? [];
  return top.map((node) => normalizeNode(node, true));
}
