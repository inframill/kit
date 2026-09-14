import { afterEach,beforeEach,expect,test } from "@jest/globals";
import { Code } from "@connectrpc/connect";
import { readFile,writeFile,access } from "node:fs/promises";
import { join } from "node:path";
import { Daemon,poll } from "./daemon.js";
import { CommandStatus,InstanceStatus } from "./gen/inframill/v1/inframill_pb.js";
let daemon:Daemon;
beforeEach(async()=>{daemon=await Daemon.create()});afterEach(async()=>{await daemon?.dispose()});
const exec=(instanceId:string,idempotencyKey="")=>({instanceId,command:"debug.record",args:{},idempotencyKey});
test("REQ001 concurrent initialization publishes exactly one complete instance",async()=>{
 const outcomes=await Promise.allSettled(Array.from({length:8},(_,i)=>daemon.client.initInstance({name:"race/dev",instance:{...daemon.instance(),values:{mode:"success",name:"winner-"+i}}})));
 const winners=outcomes.flatMap((r,i)=>r.status==="fulfilled"?[{index:i,info:r.value.instance!}]:[]);
 expect(winners).toHaveLength(1);for(const r of outcomes)if(r.status==="rejected")expect(r.reason).toMatchObject({code:Code.AlreadyExists});
 const winner=winners[0];expect(await readFile(join(daemon.root,".inframill", "instances",winner.info.id,"workspace/debug/main.txt"),"utf8")).toBe("# winner-"+winner.index+"\n");
});
test.each(["../escape","project/../escape","project/","/absolute","project/name/extra"])("REQ001 unsafe name %s is rejected",async name=>{
 await expect(daemon.init(name)).rejects.toMatchObject({code:Code.InvalidArgument});expect((await daemon.client.listInstances({})).instances).toHaveLength(0);
});
test("REQ009 crash interrupts both running and queued commands without replay",async()=>{
 const id=await daemon.init("crash/dev","block"),r=exec(id,"running"),q=exec(id,"queued");
 const running=await daemon.client.submitCommand(r);await daemon.waiting(id,running.commandId);
 const queued=await daemon.client.submitCommand(q);await daemon.command(id,queued.commandId,CommandStatus.QUEUED);
 await daemon.stop("SIGKILL");const stdout=await daemon.log(id,running.commandId,"stdout");await daemon.release(id);await daemon.start();
 for(const commandId of [running.commandId,queued.commandId]){const c=await daemon.command(id,commandId,CommandStatus.INTERRUPTED);expect(c.error?.code).toBe("COMMAND_INTERRUPTED")}
 expect((await daemon.client.submitCommand(r)).commandId).toBe(running.commandId);expect((await daemon.client.submitCommand(q)).commandId).toBe(queued.commandId);
 expect(await daemon.log(id,running.commandId,"stdout")).toBe(stdout);
 await expect(access(join(daemon.instancePath(id),"logs",queued.commandId))).rejects.toMatchObject({code:"ENOENT"});
 const fresh=await daemon.client.submitCommand(exec(id));await daemon.command(id,fresh.commandId,CommandStatus.SUCCEEDED);
});
test("REQ009 completed history and idempotency survive restart",async()=>{
 const id=await daemon.init("history/dev"),r=exec(id,"once"),first=await daemon.client.submitCommand(r);
 const original=await daemon.command(id,first.commandId,CommandStatus.SUCCEEDED);await daemon.stop();await daemon.start();
 expect(await daemon.command(id,first.commandId,CommandStatus.SUCCEEDED)).toEqual(original);
 expect((await daemon.client.submitCommand(r)).commandId).toBe(first.commandId);
});
test("REQ006 corrupt SQLite isolates a failed instance and preserves its data",async()=>{
 const bad=await daemon.init("failure/bad"),good=await daemon.init("failure/good");await daemon.stop();
 const db=join(daemon.instancePath(bad),"commands.sqlite");await writeFile(db,"corrupted fixture");await daemon.start();
 const info=(await daemon.client.getInstance({instanceId:bad})).instance!;expect(info.name).toBe("failure/bad");expect(info.status).toBe(InstanceStatus.FAILED);expect(info.failure).toBeDefined();
 for(const command of ["debug.echo","builtin.delete"])await expect(daemon.client.submitCommand({instanceId:bad,command})).rejects.toMatchObject({code:Code.FailedPrecondition});
 const accepted=await daemon.client.submitCommand(exec(good));await daemon.command(good,accepted.commandId,CommandStatus.SUCCEEDED);
 expect(await readFile(db,"utf8")).toBe("corrupted fixture");
});
test("REQ004 queue overflow is immediate and never enters history",async()=>{
 const id=await daemon.init("capacity/dev","block"),running=await daemon.client.submitCommand(exec(id));await daemon.waiting(id,running.commandId);
 for(let n=0;n<64;n++)await daemon.client.submitCommand(exec(id));
 await expect(daemon.client.submitCommand(exec(id))).rejects.toMatchObject({code:Code.Unavailable});
 expect((await daemon.client.listCommandExecutions({instanceId:id,limit:100})).executions).toHaveLength(65);
 await daemon.stop("SIGKILL");
});
test("REQ010 queued deletion rejects later work while allowing duplicate retries",async()=>{
 const id=await daemon.init("delete/barrier","block"),first=await daemon.client.submitCommand(exec(id));await daemon.waiting(id,first.commandId);
 const r={instanceId:id,command:"builtin.delete",idempotencyKey:"delete-once"},deletion=await daemon.client.submitCommand(r);
 expect((await daemon.client.submitCommand(r)).commandId).toBe(deletion.commandId);
 await expect(daemon.client.submitCommand(exec(id))).rejects.toMatchObject({code:Code.Unavailable});
 await daemon.command(id,deletion.commandId,CommandStatus.QUEUED);await daemon.release(id);
 await poll("deletion",async()=>{try{await daemon.client.getInstance({instanceId:id});return undefined}catch(e){if((e as {code:Code}).code!==Code.NotFound)throw e;return true}});
 // SPEC-0009 REQ-006: Engine allocates a new UUID and path when a name is reused.
 const replacement=await daemon.init("delete/barrier");expect(replacement).not.toBe(id);
 await expect(access(daemon.instancePath(id))).rejects.toMatchObject({code:"ENOENT"});
 await access(join(daemon.instancePath(replacement),"instance.json"));
 await expect(daemon.client.getInstance({instanceId:id})).rejects.toMatchObject({code:Code.NotFound});
});
