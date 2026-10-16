export default {
  async execute(ctx) {
    console.log('container-stdio-handler-log');
    return await ctx.platform.cache.get('container-stdio-key');
  },
};
