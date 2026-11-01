export const single = () => import('./lib/dyn-single');
export const double = () => import("./lib/dyn-double");
export const tick = () => import(`./lib/dyn-tick`);
export const hinted = () => import(/* webpackChunkName: "./lib/not-a-chunk" */ './lib/dyn-hinted');
export const opts = () => import('./lib/dyn-opts', { with: { type: 'json' } });
export const variable = (name: string) => import(name);
export const concat = (name: string) => import('./lib/not-concat' + name);
export const interpolated = (name: string) => import(`./lib/not-tick${name}`);
