const { exec } = require('child_process');
const util = require('util');
const execPromise = util.promisify(exec);

/**
 * Wrapper to call chef_solver.js from Go
 * Usage: node chef_solver_wrapper.js <username> <cookie>
 */

const ChefChallengeSolver = require('./chef_solver.js');

async function main() {
  const args = process.argv.slice(2);
  
  if (args.length < 2) {
    console.error('Usage: node chef_solver_wrapper.js <username> <cookie>');
    process.exit(1);
  }

  const [username, cookie] = args;
  const solver = new ChefChallengeSolver(`.ROBLOSECURITY=${cookie}`);
  
  try {
    await solver.solve(username);
    console.log('\n[WRAPPER] SUCCESS');
    process.exit(0);
  } catch (error) {
    console.error('\n[WRAPPER] FAILED:', error.message);
    process.exit(1);
  }
}

main();
