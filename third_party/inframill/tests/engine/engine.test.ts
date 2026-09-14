import { afterEach, beforeEach, expect, test } from "@jest/globals";
import { Code } from "@connectrpc/connect";
import { toJson } from "@bufbuild/protobuf";
import { ValueSchema } from "@bufbuild/protobuf/wkt";
import { access, rename } from "node:fs/promises";
import { join } from "node:path";
import { Daemon, poll } from "./daemon.js";
import { CommandStatus, DriverStatus, InstanceStatus, LogStream, SafetyClass } from "./gen/inframill/v1/inframill_pb.js";
let daemon: Daemon;
beforeEach(async()=>{daemon=await Daemon.create()});
afterEach(async()=>{await daemon?.dispose()});
const request=(instanceId:string)=>({instanceId,command:"debug.record",args:{}});
test("REQ002/013 metadata, command catalog and explicit fetching",async()=>{
 const {client}=daemon;const listed=await client.listBlueprints({});
 expect(listed.blueprints).toHaveLength(1);const bp=listed.blueprints[0];
 expect(bp.reference).toBe(daemon.pinnedReference);expect(bp.apiVersion).toBe("inframill.dev/blueprint/v1");
 expect(bp).not.toHaveProperty("schema");expect(bp).not.toHaveProperty("drivers");
 const ops=(await client.listCommandDefinitions({})).commands;
 expect(ops.map(o=>o.name)).toEqual(["builtin.delete","debug.echo","debug.fail","debug.logs","debug.read-artifact","debug.record","debug.secret","debug.wait","debug.write-artifact"]);
 expect(ops.find(o=>o.name==="debug.record")?.safety).toBe(SafetyClass.MUTATING);
 expect(ops.find(o=>o.name==="debug.wait")?.argumentsSchema).toHaveProperty("properties.durationMs");
 const alias=daemon.reference.replace(":latest",":alias");
 await expect(client.initInstance({name:"project/missing",instance:{...daemon.instance(),blueprint:alias}})).rejects.toThrow(/fetch this Blueprint first/);
 expect((await client.fetchBlueprint({reference:alias})).blueprint?.reference).toBe(daemon.pinnedReference);
});
test("REQ001/002 synchronous initialization validates one Values object",async()=>{
 const id=await daemon.init("project/dev");expect(id).toMatch(/^[a-f0-9-]{36}$/);
 const info=(await daemon.client.getInstance({instanceId:id})).instance!;
 expect(info.name).toBe("project/dev");expect(info.blueprint).toBe(daemon.pinnedReference);
 expect(info.values).toEqual({mode:"success",name:"test"});expect(info.status).toBe(InstanceStatus.IDLE);
 expect((await daemon.client.listCommandExecutions({instanceId:id})).executions).toHaveLength(0);
 await expect(daemon.init("project/dev")).rejects.toMatchObject({code:Code.AlreadyExists});
 await expect(daemon.init("project/invalid","invalid")).rejects.toMatchObject({code:Code.InvalidArgument});
 expect((await daemon.client.listInstances({})).instances).toHaveLength(1);
});
test("REQ004/006/007 structured success, ordinary failure and log pages",async()=>{
 const good=await daemon.init("project/good"),bad=await daemon.init("project/bad","fail");
 const first=await daemon.client.submitCommand(request(good));
 const result=await daemon.command(good,first.commandId,CommandStatus.SUCCEEDED);
 expect(result.request?.command).toBe("debug.record");expect(toJson(ValueSchema,result.result!)).toMatchObject({recorded:true,commandID:first.commandId});
 // SPEC-0000 REQ-009: tool logs merge on stderr; stdout contains only the result.
 expect(JSON.parse(await daemon.log(good,first.commandId,"stdout"))).toMatchObject({recorded:true,commandID:first.commandId});
 const log=await daemon.client.readCommandLog({instanceId:good,commandId:first.commandId,stream:LogStream.STDERR,limit:4});
 expect(Buffer.from(log.data).toString()).toBe("reco");expect(log.eof).toBe(false);
 for(let n=0;n<2;n++){
 const failed=await daemon.client.submitCommand(request(bad));
 const command=await daemon.command(bad,failed.commandId,CommandStatus.FAILED);
 expect(command.error?.code).toBe("COMMAND_FAILED");
 }
 expect((await daemon.client.getInstance({instanceId:bad})).instance?.status).toBe(InstanceStatus.IDLE);
 expect(await daemon.log(good,first.commandId,"stderr")).toContain("recorded");
});
test("REQ005/007 idempotency, scoping and pagination",async()=>{
 const id=await daemon.init("pages/a");await daemon.init("pages/b");const other=await daemon.init("other/a");
 const firstPage=await daemon.client.listInstances({project:"pages",limit:1});
 const nextPage=await daemon.client.listInstances({project:"pages",limit:1,cursor:firstPage.nextCursor});
 expect(new Set([...firstPage.instances,...nextPage.instances].map(i=>i.name))).toEqual(new Set(["pages/a","pages/b"]));
 const r={...request(id),idempotencyKey:"once"},first=await daemon.client.submitCommand(r);
 await daemon.command(id,first.commandId,CommandStatus.SUCCEEDED);
 expect((await daemon.client.submitCommand(r)).commandId).toBe(first.commandId);
 await expect(daemon.client.submitCommand({...r,command:"debug.echo"})).rejects.toMatchObject({code:Code.AlreadyExists});
 await expect(daemon.client.getCommandExecution({instanceId:other,commandId:first.commandId})).rejects.toMatchObject({code:Code.NotFound});
 const second=await daemon.client.submitCommand(request(id));await daemon.command(id,second.commandId,CommandStatus.SUCCEEDED);
 const page=await daemon.client.listCommandExecutions({instanceId:id,limit:1});expect(page.executions[0].request?.id).toBe(second.commandId);
 const next=await daemon.client.listCommandExecutions({instanceId:id,limit:1,cursor:page.nextCursor});expect(next.executions[0].request?.id).toBe(first.commandId);
 await expect(daemon.client.listCommandExecutions({instanceId:other,cursor:page.nextCursor})).rejects.toMatchObject({code:Code.InvalidArgument});
});
test("REQ004 FIFO workers allow independent instances to progress",async()=>{
 const slow=await daemon.init("queue/slow","block"),fast=await daemon.init("queue/fast");
 const first=await daemon.client.submitCommand(request(slow));await daemon.waiting(slow,first.commandId);
 const second=await daemon.client.submitCommand(request(slow));await daemon.command(slow,second.commandId,CommandStatus.QUEUED);
 const independent=await daemon.client.submitCommand(request(fast));await daemon.command(fast,independent.commandId,CommandStatus.SUCCEEDED);
 expect((await daemon.client.getInstance({instanceId:slow})).instance?.status).toBe(InstanceStatus.RUNNING);
 await daemon.release(slow);const a=await daemon.command(slow,first.commandId,CommandStatus.SUCCEEDED),b=await daemon.command(slow,second.commandId,CommandStatus.SUCCEEDED);
 expect(b.startedAt!.seconds*1000000000n+BigInt(b.startedAt!.nanos)>=a.endedAt!.seconds*1000000000n+BigInt(a.endedAt!.nanos)).toBe(true);
});
test("REQ009 instances reopen without the Blueprint store",async()=>{
 const id=await daemon.init("offline/dev");await daemon.stop();await rename(join(daemon.root,".inframill", "blueprints"),join(daemon.root,"unavailable"));await daemon.start();
 expect((await daemon.client.listBlueprints({})).blueprints).toHaveLength(0);
 const accepted=await daemon.client.submitCommand(request(id));await daemon.command(id,accepted.commandId,CommandStatus.SUCCEEDED);
});
test("REQ010 deletion removes UUID directory and all command history",async()=>{
 const id=await daemon.init("delete/dev"),first=await daemon.client.submitCommand(request(id));await daemon.command(id,first.commandId,CommandStatus.SUCCEEDED);
 await daemon.client.submitCommand({instanceId:id,command:"builtin.delete"});
 await poll("deleted",async()=>{try{await daemon.client.getInstance({instanceId:id});return undefined}catch(e){if((e as {code:Code}).code!==Code.NotFound)throw e;return true}});
 await expect(daemon.client.getCommandExecution({instanceId:id,commandId:first.commandId})).rejects.toMatchObject({code:Code.NotFound});
 await expect(access(daemon.instancePath(id))).rejects.toMatchObject({code:"ENOENT"});
 expect(await daemon.init("delete/dev")).not.toBe(id);
});

// SPEC-0007 REQ-002 / AC-002: real daemon listing reads the cached info probe.
test("SPEC-0007 Driver health is cached and exposed through the generated client", async () => {
 const first = await daemon.client.listDrivers({});
 expect(first.drivers).toHaveLength(1);
 expect(first.drivers[0]).toMatchObject({id: "debug", name: "debug", version: "0.0.0-dev", status: DriverStatus.CONNECTED, reason: ""});
 expect(first.drivers[0].checkedAt!.seconds).toBeGreaterThan(0n);
 const second = await daemon.client.listDrivers({});
 expect(second.drivers).toEqual(first.drivers);
});
