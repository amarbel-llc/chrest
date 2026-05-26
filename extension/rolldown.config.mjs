const browserType = process.env.BROWSER_TYPE;

function constsPlugin(consts) {
  const prefix = "consts:";
  return {
    name: "consts-plugin",
    resolveId(id) {
      if (id.startsWith(prefix)) return id;
    },
    load(id) {
      if (!id.startsWith(prefix)) return;
      const key = id.slice(prefix.length);
      if (!(key in consts)) {
        this.error(`Cannot find const: ${key}`);
        return;
      }
      return `export default ${JSON.stringify(consts[key])}`;
    },
  };
}

const plugins = [constsPlugin({ browserType })];

export default [
  {
    input: "src/main.js",
    output: {
      file: `dist-${browserType}/main.js`,
      format: "cjs",
    },
    plugins,
  },
  {
    input: "src/options.js",
    output: {
      file: `dist-${browserType}/options.js`,
      format: "iife",
    },
    plugins,
  },
];
